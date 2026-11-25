package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	day := time.Now().Add(-24 * time.Hour).Unix()
	users, _ := s.store.CountUsers(ctx)
	sessions, _ := s.store.CountSessions(ctx)
	shares, _ := s.store.CountActiveShares(ctx)
	uploads, _ := s.store.CountUploads(ctx)
	shareDl, _ := s.store.AuditCountSince(ctx, day, "share.download")
	shareUp, _ := s.store.AuditCountSince(ctx, day, "share.upload")
	failed, _ := s.store.AuditCountSince(ctx, day, "login.failed")
	drives := []driveJSON{}
	for _, m := range s.driveList() {
		drives = append(drives, s.driveOut(ctx, m))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"system": s.mon.Snapshot(),
		"drives": drives,
		"stats": map[string]int{
			"users": users, "sessions": sessions, "activeShares": shares, "uploadsInProgress": uploads,
			"shareDownloads24h": shareDl, "shareUploads24h": shareUp, "failedLogins24h": failed,
		},
		"version": s.version,
		"update":  s.updates.Status(s.updatesOn()),
		"media":   s.mediaStatus(ctx),
		"mail":    s.mailEnabled(),
		"health":  s.healthProblems(ctx),
	})
}

func (s *Server) mediaStatus(ctx context.Context) map[string]any {
	jobs, _ := s.store.MediaStats(ctx)
	if jobs == nil {
		jobs = map[string]int{}
	}
	return map[string]any{"workerOnline": s.workerOnline(ctx), "jobs": jobs}
}

func (s *Server) updatesOn() bool { return s.cfg.UpdatesAllowed && s.cur().UpdateCheck }

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request, _ *store.User) {
	writeJSON(w, http.StatusOK, s.updates.Status(s.updatesOn()))
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request, _ *store.User) {
	if !s.cfg.UpdatesAllowed {
		forbidden(w, "update checks are disabled by COFFER_UPDATE_CHECK")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, _ = s.updates.Check(ctx)
	writeJSON(w, http.StatusOK, s.updates.Status(s.updatesOn()))
}

// updateLoop checks for a new release twice a day and tells admins once.
func (s *Server) updateLoop(ctx context.Context) {
	told := ""
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		if s.updatesOn() {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if rel, _ := s.updates.Check(cctx); rel != nil && rel.Version != told {
				told = rel.Version
				s.notifyAdmins("update", "Coffer "+rel.Version+" is available",
					"You are running "+s.version+". Update with: docker compose pull && docker compose up -d", "/admin/monitor")
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// handleMetrics serves Prometheus metrics when COFFER_METRICS_TOKEN is set.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	tok := s.cfg.MetricsToken
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
		http.NotFound(w, r)
		return
	}
	extra := map[string]float64{}
	ctx := r.Context()
	for _, m := range s.driveList() {
		si := s.index.ScanInfo(ctx, m.info.ID)
		id := strings.ReplaceAll(m.info.ID, "-", "_")
		extra["coffer_drive_"+id+"_bytes"] = float64(si.Bytes)
		extra["coffer_drive_"+id+"_files"] = float64(si.Files)
		online := 0.0
		if m.d != nil {
			online = 1
			if free, total, ok := m.d.Usage(ctx); ok {
				extra["coffer_drive_"+id+"_free_bytes"] = float64(free)
				extra["coffer_drive_"+id+"_total_bytes"] = float64(total)
			}
		}
		extra["coffer_drive_"+id+"_online"] = online
	}
	if n, err := s.store.CountSessions(ctx); err == nil {
		extra["coffer_sessions"] = float64(n)
	}
	if n, err := s.store.CountActiveShares(ctx); err == nil {
		extra["coffer_active_shares"] = float64(n)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.mon.WritePrometheus(w, extra)
}
