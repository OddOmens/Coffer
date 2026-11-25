// Package server is Coffer's HTTP API, WebDAV endpoint and web UI host.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oddomens/coffer/internal/config"
	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/monitor"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
	"github.com/oddomens/coffer/internal/updates"
)

type Server struct {
	cfg     *config.Config
	store   *store.Store
	index   *index.Index
	thumbs  *files.Thumbnailer
	keys    *security.Keys
	log     *slog.Logger
	version string
	spa     http.Handler
	mon     *monitor.Monitor
	updates *updates.Checker
	drives  driveSet
	snaps   snapshotCache
	health  healthTracker

	settings atomic.Pointer[store.Settings]

	apiLimit   *security.Limiter // every request, per IP
	loginLimit *security.Limiter // login attempts, per IP
	shareMiss  *security.Limiter // unknown share tokens, per IP
	shareAuth  *security.Limiter // share password attempts, per share
	backoff    *security.Backoff // failed logins, per user, share+IP and WebDAV IP
}

type Options struct {
	Config  *config.Config
	Store   *store.Store
	Index   *index.Index
	Keys    *security.Keys
	Log     *slog.Logger
	Version string
	WebUI   fs.FS
	Worker  bool // the media worker process: no listeners, leave the server's scratch files alone
}

func New(o Options) (*Server, error) {
	thumbs, err := files.NewThumbnailer(filepath.Join(o.Config.DataDir, "thumbs"))
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg: o.Config, store: o.Store, index: o.Index, thumbs: thumbs, keys: o.Keys, log: o.Log, version: o.Version,
		spa:        newSPA(o.WebUI),
		mon:        monitor.New(),
		updates:    updates.New(o.Config.UpdateRepo, o.Version),
		apiLimit:   security.NewLimiter(1800, 600),
		loginLimit: security.NewLimiter(10, 10),
		shareMiss:  security.NewLimiter(20, 20),
		shareAuth:  security.NewLimiter(10, 10),
		backoff:    security.NewBackoff(5),
	}
	st, err := o.Store.Settings(context.Background())
	if err != nil {
		return nil, err
	}
	s.settings.Store(&st)
	if err := s.loadDrives(context.Background()); err != nil {
		return nil, err
	}
	if !o.Worker {
		_ = os.RemoveAll(filepath.Join(o.Config.DataDir, "dav-tmp"))
	}
	return s, nil
}

// Close releases every drive.
func (s *Server) Close() {
	for _, m := range s.driveList() {
		if m.d != nil {
			m.d.Close()
		}
	}
}

func (s *Server) cur() store.Settings { return *s.settings.Load() }

// ConnState counts open connections for monitoring.
func (s *Server) ConnState(_ net.Conn, st http.ConnState) {
	switch st {
	case http.StateNew:
		s.mon.Conns.Add(1)
	case http.StateClosed, http.StateHijacked:
		s.mon.Conns.Add(-1)
	}
}

// AppHandler serves the full app. Clients outside COFFER_APP_NETWORKS only
// get the public (share links) surface.
func (s *Server) AppHandler() http.Handler {
	app := http.NewServeMux()
	s.publicRoutes(app, false)
	s.appRoutes(app)
	pub := http.NewServeMux()
	s.publicRoutes(pub, true)

	appH, pubH := s.protect(app), s.protect(pub)
	return s.realIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AppAllowed(clientIP(r)) {
			appH.ServeHTTP(w, r)
		} else {
			pubH.ServeHTTP(w, r)
		}
	}), s.cfg.IsAppProxy)
}

// PublicHandler serves share links only.
func (s *Server) PublicHandler() http.Handler {
	pub := http.NewServeMux()
	s.publicRoutes(pub, true)
	return s.realIP(s.protect(pub), s.cfg.IsTrustedProxy)
}

func (s *Server) protect(h http.Handler) http.Handler {
	// Rejects cross-site state-changing requests (Sec-Fetch-Site / Origin).
	cop := http.NewCrossOriginProtection()
	return s.logRequests(securityHeaders(s.rateLimit(cop.Handler(gzipJSON(h, skipGzip)))))
}

// skipGzip names the routes that send files, previews and archives, which
// keep the plain writer (and its sendfile path) whatever they answer with.
func skipGzip(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/auth/") {
		return true
	}
	switch p[strings.LastIndexByte(p, '/')+1:] {
	case "download", "thumb", "media", "zip", "file", "logo":
		return true
	}
	return false
}

func (s *Server) publicRoutes(mux *http.ServeMux, publicOnly bool) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/config", s.handleConfig(publicOnly))
	mux.HandleFunc("GET /api/branding/logo", s.handleLogo)

	mux.HandleFunc("GET /api/s/{token}", s.shareHandler(s.handleShareMeta))
	mux.HandleFunc("POST /api/s/{token}/auth", s.shareHandler(s.handleShareAuth))
	mux.HandleFunc("GET /api/s/{token}/list", s.shareHandler(s.handleShareList))
	mux.HandleFunc("GET /api/s/{token}/download", s.shareHandler(s.handleShareDownload))
	mux.HandleFunc("GET /api/s/{token}/thumb", s.shareHandler(s.handleShareThumb))
	mux.HandleFunc("GET /api/s/{token}/zip", s.shareHandler(s.handleShareZip))
	mux.HandleFunc("POST /api/s/{token}/uploads", s.shareHandler(s.handleShareUploadCreate))
	mux.HandleFunc("GET /api/s/{token}/uploads/{id}", s.shareHandler(s.handleShareUploadStatus))
	mux.HandleFunc("PATCH /api/s/{token}/uploads/{id}", s.shareHandler(s.handleShareUploadChunk))
	mux.HandleFunc("POST /api/s/{token}/uploads/{id}/complete", s.shareHandler(s.handleShareUploadComplete))
	mux.HandleFunc("DELETE /api/s/{token}/uploads/{id}", s.shareHandler(s.handleShareUploadAbort))
	mux.HandleFunc("POST /api/s/{token}/verify", s.shareHandler(s.handleShareVerifyStart))
	mux.HandleFunc("POST /api/s/{token}/verify/check", s.shareHandler(s.handleShareVerifyCheck))
	mux.HandleFunc("POST /api/s/{token}/verify/invite", s.shareHandler(s.handleShareVerifyInvite))
	mux.HandleFunc("POST /api/s/{token}/forget", s.shareHandler(s.handleShareForget))
	mux.HandleFunc("GET /api/s/{token}/media", s.shareHandler(s.handleShareMedia))
	mux.HandleFunc("GET /api/s/{token}/comments", s.shareHandler(s.handleShareComments))
	mux.HandleFunc("POST /api/s/{token}/comments", s.shareHandler(s.handleShareCommentCreate))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	})
	mux.Handle("/", s.spa)
}

func (s *Server) appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dav/", s.handleDAV)
	mux.HandleFunc("/dav", s.handleDAV)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	mux.HandleFunc("GET /api/me", s.authed(s.handleMe))
	mux.HandleFunc("GET /api/me/prefs", s.authed(s.handlePrefsGet))
	mux.HandleFunc("PUT /api/me/prefs", s.authed(s.handlePrefsPut))
	mux.HandleFunc("POST /api/me/password", s.authed(s.handleChangePassword))
	mux.HandleFunc("POST /api/me/totp/setup", s.authed(s.handleTOTPSetup))
	mux.HandleFunc("POST /api/me/totp/enable", s.authed(s.handleTOTPEnable))
	mux.HandleFunc("POST /api/me/totp/disable", s.authed(s.handleTOTPDisable))
	mux.HandleFunc("POST /api/me/recovery", s.authed(s.handleRecoveryCodes))
	mux.HandleFunc("GET /api/me/sessions", s.authed(s.handleSessions))
	mux.HandleFunc("DELETE /api/me/sessions/{id}", s.authed(s.handleRevokeSession))
	mux.HandleFunc("GET /api/me/tokens", s.authed(s.handleTokensList))
	mux.HandleFunc("POST /api/me/tokens", s.authed(s.handleTokenCreate))
	mux.HandleFunc("DELETE /api/me/tokens/{id}", s.authed(s.handleTokenDelete))
	mux.HandleFunc("PUT /api/me/email", s.authed(s.handleMyEmail))
	mux.HandleFunc("GET /api/notifications", s.authed(s.handleNotifications))
	mux.HandleFunc("POST /api/notifications/read", s.authed(s.handleNotificationsRead))

	mux.HandleFunc("GET /api/places", s.authed(s.handlePlaces))
	mux.HandleFunc("POST /api/places/share", s.authed(s.handleGrant))
	mux.HandleFunc("GET /api/places/granted", s.authed(s.handleGrantsList))
	mux.HandleFunc("PATCH /api/places/{id}", s.authed(s.handleGrantUpdate))
	mux.HandleFunc("DELETE /api/places/{id}", s.authed(s.handlePlaceDelete))
	mux.HandleFunc("GET /api/users/directory", s.authed(s.handleUserDirectory))

	mux.HandleFunc("GET /api/files/list", s.authed(s.handleList))
	mux.HandleFunc("GET /api/files/stat", s.authed(s.handleStat))
	mux.HandleFunc("GET /api/files/download", s.authed(s.handleDownload))
	mux.HandleFunc("GET /api/files/thumb", s.authed(s.handleThumb))
	mux.HandleFunc("GET /api/files/media", s.authed(s.handleMedia))
	mux.HandleFunc("GET /api/files/comments", s.authed(s.handleCommentsList))
	mux.HandleFunc("POST /api/files/comments", s.authed(s.handleCommentCreate))
	mux.HandleFunc("PATCH /api/comments/{id}", s.authed(s.handleCommentUpdate))
	mux.HandleFunc("DELETE /api/comments/{id}", s.authed(s.handleCommentDelete))
	mux.HandleFunc("GET /api/files/activity", s.authed(s.handleActivity))
	mux.HandleFunc("POST /api/files/lock", s.authed(s.handleLock))
	mux.HandleFunc("DELETE /api/files/lock", s.authed(s.handleUnlock))
	mux.HandleFunc("POST /api/files/tags", s.authed(s.handleTagsEdit))
	mux.HandleFunc("GET /api/tags", s.authed(s.handleTagsList))
	mux.HandleFunc("GET /api/tags/{tag}", s.authed(s.handleTagItems))
	mux.HandleFunc("PUT /api/tags/{tag}/color", s.authed(s.handleTagColor))
	mux.HandleFunc("GET /api/favorites", s.authed(s.handleFavorites))
	mux.HandleFunc("PUT /api/favorites", s.authed(s.handleFavoriteSet))
	mux.HandleFunc("GET /api/collections", s.authed(s.handleCollectionsList))
	mux.HandleFunc("POST /api/collections", s.authed(s.handleCollectionCreate))
	mux.HandleFunc("PATCH /api/collections/{id}", s.authed(s.handleCollectionUpdate))
	mux.HandleFunc("DELETE /api/collections/{id}", s.authed(s.handleCollectionDelete))
	mux.HandleFunc("POST /api/collections/{id}/items", s.authed(s.handleCollectionAdd))
	mux.HandleFunc("POST /api/collections/{id}/items/remove", s.authed(s.handleCollectionRemove))
	mux.HandleFunc("GET /api/files/zip", s.authed(s.handleZip))
	mux.HandleFunc("GET /api/files/text", s.authed(s.handleTextGet))
	mux.HandleFunc("PUT /api/files/text", s.authed(s.handleTextPut))
	mux.HandleFunc("GET /api/files/breakdown", s.authed(s.handleBreakdown))
	mux.HandleFunc("POST /api/files/mkdir", s.authed(s.handleMkdir))
	mux.HandleFunc("POST /api/files/rename", s.authed(s.handleRename))
	mux.HandleFunc("POST /api/files/move", s.authed(s.handleMove))
	mux.HandleFunc("POST /api/files/copy", s.authed(s.handleCopy))
	mux.HandleFunc("POST /api/files/delete", s.authed(s.handleDelete))
	mux.HandleFunc("GET /api/search", s.authed(s.handleSearch))
	mux.HandleFunc("GET /api/recent", s.authed(s.handleRecent))

	mux.HandleFunc("POST /api/uploads", s.authed(s.handleUploadCreate))
	mux.HandleFunc("GET /api/uploads/{id}", s.authed(s.handleUploadStatus))
	mux.HandleFunc("PATCH /api/uploads/{id}", s.authed(s.handleUploadChunk))
	mux.HandleFunc("POST /api/uploads/{id}/complete", s.authed(s.handleUploadComplete))
	mux.HandleFunc("DELETE /api/uploads/{id}", s.authed(s.handleUploadAbort))

	mux.HandleFunc("GET /api/trash", s.authed(s.handleTrashList))
	mux.HandleFunc("POST /api/trash/{id}/restore", s.authed(s.handleTrashRestore))
	mux.HandleFunc("DELETE /api/trash/{id}", s.authed(s.handleTrashPurge))
	mux.HandleFunc("DELETE /api/trash", s.authed(s.handleTrashEmpty))

	mux.HandleFunc("GET /api/shares", s.authed(s.handleSharesList))
	mux.HandleFunc("POST /api/shares", s.authed(s.handleShareCreate))
	mux.HandleFunc("PATCH /api/shares/{id}", s.authed(s.handleShareUpdate))
	mux.HandleFunc("DELETE /api/shares/{id}", s.authed(s.handleShareDelete))
	mux.HandleFunc("POST /api/shares/{id}/invite", s.authed(s.handleShareInvite))
	mux.HandleFunc("PATCH /api/shares/{id}/recipients/{rid}", s.authed(s.handleRecipientUpdate))
	mux.HandleFunc("DELETE /api/shares/{id}/recipients/{rid}", s.authed(s.handleRecipientDelete))

	mux.HandleFunc("GET /api/admin/users", s.admin(s.handleUsersList))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.handleUserCreate))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.handleUserUpdate))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.admin(s.handleUserDelete))
	mux.HandleFunc("GET /api/admin/users/{id}/places", s.admin(s.handleUserPlaces))
	mux.HandleFunc("POST /api/admin/users/{id}/places", s.admin(s.handleUserPlaceCreate))
	mux.HandleFunc("PUT /api/admin/places/{id}", s.admin(s.handleAdminPlaceUpdate))
	mux.HandleFunc("GET /api/admin/drives", s.admin(s.handleDrivesList))
	mux.HandleFunc("POST /api/admin/drives", s.admin(s.handleDriveCreate))
	mux.HandleFunc("POST /api/admin/drives/test", s.admin(s.handleDriveTest))
	mux.HandleFunc("PATCH /api/admin/drives/{id}", s.admin(s.handleDriveUpdate))
	mux.HandleFunc("DELETE /api/admin/drives/{id}", s.admin(s.handleDriveDelete))
	mux.HandleFunc("POST /api/admin/drives/{id}/rescan", s.admin(s.handleDriveRescan))
	mux.HandleFunc("POST /api/admin/drives/{id}/recovery-key", s.admin(s.handleDriveRecoveryKey))
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.handleSettingsGet))
	mux.HandleFunc("PUT /api/admin/settings", s.admin(s.handleSettingsPut))
	mux.HandleFunc("POST /api/admin/email/test", s.admin(s.handleTestEmail))
	mux.HandleFunc("GET /api/admin/backups", s.admin(s.handleBackupsList))
	mux.HandleFunc("POST /api/admin/backups", s.admin(s.handleBackupCreate))
	mux.HandleFunc("POST /api/admin/backups/items", s.admin(s.handleBackupAdd))
	mux.HandleFunc("PATCH /api/admin/backups/{id}", s.admin(s.handleBackupUpdate))
	mux.HandleFunc("DELETE /api/admin/backups/{id}", s.admin(s.handleBackupDelete))
	mux.HandleFunc("POST /api/admin/backups/{id}/run", s.admin(s.handleBackupRun))
	mux.HandleFunc("GET /api/admin/backups/{id}/runs", s.admin(s.handleBackupRuns))
	mux.HandleFunc("GET /api/admin/backups/{id}/snapshots", s.admin(s.handleBackupSnapshots))
	mux.HandleFunc("GET /api/admin/backups/{id}/snapshots/{snap}", s.admin(s.handleBackupBrowse))
	mux.HandleFunc("GET /api/admin/backups/{id}/snapshots/{snap}/file", s.admin(s.handleBackupFile))
	mux.HandleFunc("GET /api/admin/backups/{id}/snapshots/{snap}/zip", s.admin(s.handleBackupZip))
	mux.HandleFunc("POST /api/admin/backups/{id}/restore", s.admin(s.handleBackupRestore))
	mux.HandleFunc("PUT /api/admin/logo", s.admin(s.handleLogoUpload))
	mux.HandleFunc("DELETE /api/admin/logo", s.admin(s.handleLogoDelete))
	mux.HandleFunc("GET /api/admin/audit", s.admin(s.handleAudit))
	mux.HandleFunc("GET /api/admin/monitor", s.admin(s.handleMonitor))
	mux.HandleFunc("GET /api/admin/health", s.admin(s.handleHealth))
	mux.HandleFunc("GET /api/admin/updates", s.admin(s.handleUpdateStatus))
	mux.HandleFunc("POST /api/admin/updates/check", s.admin(s.handleUpdateCheck))
}

func (s *Server) handleConfig(publicOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := s.cur()
		var logoVersion int64
		if info, err := os.Stat(s.logoPath()); err == nil && st.LogoType != "" {
			logoVersion = info.ModTime().Unix()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"appName":         st.AppName,
			"accentColor":     st.AccentColor,
			"loginMessage":    st.LoginMessage,
			"hasLogo":         st.LogoType != "",
			"logoVersion":     logoVersion,
			"publicOnly":      publicOnly,
			"version":         s.version,
			"defaultTheme":    st.DefaultTheme,
			"defaultDensity":  st.DefaultDensity,
			"showQuickCreate": st.ShowQuickCreate,
			"webdav":          st.WebDAV && !publicOnly,
		})
	}
}

func (s *Server) audit(r *http.Request, u *store.User, shareID, action, path, detail string) {
	e := store.AuditEntry{ShareID: shareID, IP: ipString(r), Action: action, Path: path, Detail: detail}
	if u != nil {
		e.UserID, e.Username = u.ID, u.Username
	}
	if err := s.store.Audit(context.WithoutCancel(r.Context()), e); err != nil {
		s.log.Error("audit write failed", "err", err)
	}
	// Sign-ins also go to the server log, so a log watcher can alert on them.
	switch action {
	case "login":
		s.log.Info("signed in", "user", e.Username, "ip", e.IP)
	case "login.failed":
		s.log.Warn("sign-in failed", "ip", e.IP, "detail", detail)
	case "webdav.denied":
		s.log.Warn("sign-in failed", "ip", e.IP, "detail", detail, "via", "webdav")
	}
}

// RunJobs runs background work (index scans, update checks, cleanup) until
// ctx is cancelled.
func (s *Server) RunJobs(ctx context.Context) {
	go s.scanLoop(ctx)
	go s.watchDrives(ctx)
	go s.cleanStaleWrites(ctx)
	go s.updateLoop(ctx)
	go s.backupLoop(ctx)
	go s.healthLoop(ctx)
	if MediaToolsInstalled() {
		go s.RunMediaWorker(ctx)
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) cleanup(ctx context.Context) {
	now := time.Now()
	if err := s.store.PurgeSessions(ctx, now.Add(-s.cfg.SessionIdle).Unix()); err != nil {
		s.log.Error("purge sessions", "err", err)
	}
	if ups, err := s.store.UploadsBefore(ctx, now.Add(-48*time.Hour).Unix()); err == nil {
		for _, up := range ups {
			if m, err := s.drive(up.DriveID); err == nil {
				_ = m.d.AbortUpload(ctx, uploadState(up))
			}
			_ = s.store.DeleteUpload(ctx, up.ID)
		}
	}
	if ups, err := s.store.UploadsBefore(ctx, 1<<62); err == nil {
		known := map[string]bool{}
		for _, up := range ups {
			known[up.ID] = true
		}
		for _, m := range s.driveList() {
			if l, ok := m.d.(*files.Local); ok {
				for _, id := range l.OrphanUploads(known) {
					_ = l.AbortUpload(ctx, &files.UploadState{ID: id})
				}
			}
		}
	}
	if days := s.cur().TrashDays; days > 0 {
		cutoff := now.AddDate(0, 0, -days).Unix()
		if items, err := s.store.TrashItems(ctx); err == nil {
			for _, it := range items {
				if it.DeletedAt >= cutoff {
					continue
				}
				if m, err := s.drive(it.DriveID); err == nil {
					if err := m.d.PurgeTrash(ctx, it.ID); err == nil {
						_ = s.store.DeleteTrash(ctx, it.ID)
					}
				}
			}
		}
	}
	if err := s.store.PurgeAudit(ctx, now.AddDate(-1, 0, 0).Unix()); err != nil {
		s.log.Error("purge audit", "err", err)
	}
	_ = s.store.PurgeNotifications(ctx, now.AddDate(0, 0, -90).Unix())
	_ = s.store.PurgeShareCodes(ctx)
	s.cleanMediaTmp()
	s.thumbs.PruneWatermarked(48 * time.Hour)
	if time.Since(s.health.pruned) > 23*time.Hour {
		s.health.pruned = now
		s.prune(ctx, now)
	}
}
