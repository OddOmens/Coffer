package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// mounted is a configured drive and its open backend (nil when offline).
type mounted struct {
	info *store.Drive
	d    files.Drive
	err  error
	path string // local drives: directory on the server
}

type driveSet struct {
	mu sync.RWMutex
	m  map[string]*mounted
}

type localConfig struct {
	Path string `json:"path"`
}

// s3Stored is the S3 config as saved: the secret key and, on an encrypted
// drive, the drive key are sealed with the instance key.
type s3Stored struct {
	files.S3Config
	SecretSealed string `json:"secretSealed"`
	KeySealed    string `json:"keySealed,omitempty"`
}

func (s *Server) openDrive(ctx context.Context, d *store.Drive, all []*store.Drive) *mounted {
	m := s.mountDrive(ctx, d, all)
	if m.err != nil {
		s.log.Error("drive offline", "drive", d.Name, "err", m.err)
	}
	return m
}

// mountDrive is openDrive without logging. all is every configured drive.
func (s *Server) mountDrive(ctx context.Context, d *store.Drive, all []*store.Drive) *mounted {
	m := &mounted{info: d}
	if d.Disabled {
		m.err = errors.New("drive is disabled")
		return m
	}
	switch d.Kind {
	case "local":
		m.path = s.localDir(d)
		var shared []string
		for _, o := range all {
			if o.ID != d.ID && o.Kind == "local" && s.localDir(o) == m.path {
				shared = append(shared, o.ID)
			}
		}
		// A folder without its marker is only taken as the drive when it
		// holds files, or the drive had none: an empty folder where the
		// index remembers files is a disk that isn't mounted.
		si := s.index.ScanInfo(ctx, d.ID)
		l, err := files.OpenLocalDrive(m.path, d.ID, si.Files == 0 && si.Dirs == 0, shared)
		if err == nil {
			m.d = l
		}
		m.err = err
	case "s3":
		cfg, err := s.s3Config(d)
		if err != nil {
			m.err = err
			break
		}
		m.d, m.err = files.OpenS3(cfg)
	default:
		m.err = fmt.Errorf("unknown drive type %q", d.Kind)
	}
	if m.err != nil {
		// A failed Open returns a typed nil, which would pass for online.
		m.d = nil
	}
	return m
}

// localDir is a local drive's folder on the server.
func (s *Server) localDir(d *store.Drive) string {
	var c localConfig
	_ = json.Unmarshal([]byte(d.Config), &c)
	if c.Path == "" {
		return s.cfg.StorageDir
	}
	return c.Path
}

func (s *Server) s3Config(d *store.Drive) (files.S3Config, error) {
	var st s3Stored
	if err := json.Unmarshal([]byte(d.Config), &st); err != nil {
		return files.S3Config{}, err
	}
	secret, err := s.keys.Decrypt(st.SecretSealed)
	if err != nil {
		return files.S3Config{}, errors.New("cannot decrypt the stored secret key (was the instance key changed?)")
	}
	st.SecretKey = string(secret)
	if st.Encrypt {
		key, err := s.keys.Decrypt(st.KeySealed)
		if err != nil || len(key) != files.DriveKeySize {
			return files.S3Config{}, errors.New("cannot decrypt the drive's encryption key (was the instance key changed?): edit the drive and enter its recovery key")
		}
		st.Key = key
	}
	return st.S3Config, nil
}

// s3Public is the S3 config without any secrets, for display. It needs no
// keys, so it works even when they can't be decrypted.
func s3Public(d *store.Drive) *files.S3Config {
	var st s3Stored
	if json.Unmarshal([]byte(d.Config), &st) != nil {
		return nil
	}
	c := st.S3Config
	c.SecretKey, c.Key = "", nil
	return &c
}

// encrypted reports whether a drive seals its files before they reach the
// bucket.
func encrypted(m *mounted) bool {
	if m.info.Kind != "s3" {
		return false
	}
	c := s3Public(m.info)
	return c != nil && c.Encrypt
}

func (s *Server) loadDrives(ctx context.Context) error {
	list, err := s.store.Drives(ctx)
	if err != nil {
		return err
	}
	m := map[string]*mounted{}
	for _, d := range list {
		m[d.ID] = s.openDrive(ctx, d, list)
	}
	s.drives.mu.Lock()
	old := s.drives.m
	s.drives.m = m
	s.drives.mu.Unlock()
	for _, o := range old {
		if o.d != nil {
			o.d.Close()
		}
	}
	return nil
}

var errDriveOffline = errors.New("drive offline")

// watchDrives notices local drives that go away (an unplugged disk, an
// unmounted share) and come back, so nothing is read from or written to an
// empty mount point in the meantime.
func (s *Server) watchDrives(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, m := range s.driveList() {
			_, _ = s.checkDrive(ctx, m.info.ID)
		}
	}
}

// checkDrive looks again at a local drive's folder, takes the drive offline
// when its marker is gone, and back online when it returns. It returns the
// drive as s.drive does. Other drives are returned as they are.
func (s *Server) checkDrive(ctx context.Context, id string) (*mounted, error) {
	m, err := s.drive(id)
	if m == nil || m.info.Kind != "local" || m.info.Disabled {
		return m, err
	}
	if m.d != nil {
		l, ok := m.d.(*files.Local)
		if !ok {
			return m, nil
		}
		cerr := l.Check(m.info.ID)
		if cerr == nil {
			return m, nil
		}
		if !errors.Is(cerr, files.ErrRemounted) {
			if s.swapDrive(m, &mounted{info: m.info, path: m.path, err: cerr}) {
				s.log.Error("drive went offline", "drive", m.info.Name, "err", cerr)
			}
			return s.drive(id)
		}
		// Mounted again: open the new folder below.
	}
	var all []*store.Drive
	for _, o := range s.driveList() {
		all = append(all, o.info)
	}
	nm := s.mountDrive(ctx, m.info, all)
	if nm.d == nil {
		if m.d != nil || fmt.Sprint(m.err) != fmt.Sprint(nm.err) {
			s.swapDrive(m, nm)
		}
		return s.drive(id)
	}
	if s.swapDrive(m, nm) {
		if m.d == nil {
			s.log.Info("drive back online", "drive", m.info.Name)
			go s.rescan(context.WithoutCancel(ctx), nm)
		}
	}
	return s.drive(id)
}

// swapDrive replaces old with nm unless the drives were reloaded since, and
// closes whichever handle is no longer used.
func (s *Server) swapDrive(old, nm *mounted) bool {
	s.drives.mu.Lock()
	ok := s.drives.m[old.info.ID] == old
	if ok {
		s.drives.m[old.info.ID] = nm
	}
	s.drives.mu.Unlock()
	switch {
	case ok && old.d != nil:
		old.d.Close()
	case !ok && nm.d != nil:
		nm.d.Close()
	}
	return ok
}

// cleanStaleWrites removes temporary files that writes cut short by a
// restart left on local drives.
func (s *Server) cleanStaleWrites(ctx context.Context) {
	for _, m := range s.driveList() {
		l, ok := m.d.(*files.Local)
		if !ok {
			continue
		}
		n, err := l.CleanStaleWrites(ctx, 24*time.Hour)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("cleaning unfinished writes failed", "drive", m.info.Name, "err", err)
		}
		if n > 0 {
			s.log.Info("removed unfinished writes", "drive", m.info.Name, "files", n)
		}
	}
}

func (s *Server) drive(id string) (*mounted, error) {
	s.drives.mu.RLock()
	defer s.drives.mu.RUnlock()
	m, ok := s.drives.m[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	if m.d == nil {
		return m, fmt.Errorf("%w: %v", errDriveOffline, m.err)
	}
	return m, nil
}

func (s *Server) driveList() []*mounted {
	s.drives.mu.RLock()
	defer s.drives.mu.RUnlock()
	out := make([]*mounted, 0, len(s.drives.m))
	for _, m := range s.drives.m {
		out = append(out, m)
	}
	sortMounted(out)
	return out
}

func sortMounted(ms []*mounted) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && (ms[j].info.Sort < ms[j-1].info.Sort ||
			(ms[j].info.Sort == ms[j-1].info.Sort && strings.ToLower(ms[j].info.Name) < strings.ToLower(ms[j-1].info.Name))); j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}

// scanLoop keeps every drive's index fresh.
func (s *Server) scanLoop(ctx context.Context) {
	last := map[string]time.Time{}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		st := s.cur()
		for _, m := range s.driveList() {
			if m.d == nil {
				continue
			}
			every := time.Duration(st.ScanMinutes) * time.Minute
			if m.info.Kind == "s3" {
				every = time.Duration(st.ScanMinutesS3) * time.Minute
			}
			info := s.index.ScanInfo(ctx, m.info.ID)
			if t0, ok := last[m.info.ID]; ok && (every <= 0 || time.Since(t0) < every) {
				continue
			} else if !ok && info.Finished > 0 && info.Error == "" && every > 0 &&
				time.Since(time.Unix(info.Finished, 0)) < every {
				last[m.info.ID] = time.Unix(info.Finished, 0)
				continue
			}
			last[m.info.ID] = time.Now()
			s.rescan(ctx, m)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) rescan(ctx context.Context, m *mounted) {
	// Never index an empty mount point as the drive having no files.
	if m.info.Kind == "local" {
		var err error
		if m, err = s.checkDrive(ctx, m.info.ID); err != nil {
			return
		}
	}
	start := time.Now()
	err := s.index.Scan(ctx, m.info.ID, m.d)
	if errors.Is(err, index.ErrBusy) {
		return
	}
	if err != nil && ctx.Err() == nil {
		s.log.Error("index scan failed", "drive", m.info.Name, "err", err)
		return
	}
	s.log.Info("indexed drive", "drive", m.info.Name, "ms", time.Since(start).Milliseconds())
}

// reindex updates the index after Coffer changed dir (non-recursive).
func (s *Server) reindex(m *mounted, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.index.Sync(ctx, m.info.ID, m.d, dir); err != nil {
		s.log.Warn("index sync failed", "drive", m.info.Name, "dir", dir, "err", err)
	}
}

// Admin drive management.

type driveJSON struct {
	*store.Drive
	Online bool            `json:"online"`
	Error  string          `json:"error,omitempty"`
	Path   string          `json:"path,omitempty"`
	S3     *files.S3Config `json:"s3,omitempty"`
	Free   int64           `json:"free"`
	Total  int64           `json:"total"`
	Scan   index.ScanInfo  `json:"scan"`
	// Only right after the drive key is made or replaced.
	RecoveryKey string `json:"recoveryKey,omitempty"`
}

func (s *Server) driveOut(ctx context.Context, m *mounted) driveJSON {
	out := driveJSON{Drive: m.info, Online: m.d != nil, Path: m.path, Scan: s.index.ScanInfo(ctx, m.info.ID)}
	if m.err != nil {
		out.Error = m.err.Error()
	}
	if m.info.Kind == "s3" {
		out.S3 = s3Public(m.info)
	}
	if m.d != nil {
		if free, total, ok := m.d.Usage(ctx); ok {
			out.Free, out.Total = free, total
		}
	}
	return out
}

func (s *Server) handleDrivesList(w http.ResponseWriter, r *http.Request, _ *store.User) {
	out := []driveJSON{}
	for _, m := range s.driveList() {
		out = append(out, s.driveOut(r.Context(), m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"drives": out, "roots": s.cfg.DriveRoots})
}

type driveReq struct {
	Name     *string         `json:"name"`
	Kind     string          `json:"kind"`
	Path     string          `json:"path"`
	S3       *files.S3Config `json:"s3"`
	Sort     *int            `json:"sort"`
	Disabled *bool           `json:"disabled"`
	// For an encrypted bucket folder that Coffer set up before.
	RecoveryKey string `json:"recoveryKey"`
}

var driveNameRe = regexp.MustCompile(`^[\p{L}\p{N} ._()&'-]{1,48}$`)

// localPath checks a requested local drive directory against the allowed roots.
func (s *Server) localPath(p string) (string, error) {
	p = filepath.Clean(strings.TrimSpace(p))
	if !filepath.IsAbs(p) {
		return "", errors.New("use an absolute path inside the container, such as /srv/projects")
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", errors.New("that folder does not exist inside the container; mount it in docker-compose.yml first")
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return "", errors.New("that path is not a folder")
	}
	if real == "/" {
		return "", errors.New("a drive cannot be the whole filesystem")
	}
	for _, sys := range []string{"/proc", "/sys", "/dev"} {
		if pathWithin(sys, real) {
			return "", errors.New("system folders cannot be drives")
		}
	}
	data, err := filepath.EvalSymlinks(s.cfg.DataDir)
	if err != nil {
		data, _ = filepath.Abs(s.cfg.DataDir)
	}
	if data != "" && (pathWithin(data, real) || pathWithin(real, data)) {
		return "", errors.New("a drive cannot contain Coffer's data folder")
	}
	for _, root := range s.cfg.DriveRoots {
		if rr, err := filepath.EvalSymlinks(root); err == nil && pathWithin(rr, real) {
			return real, nil
		}
	}
	return "", fmt.Errorf("local drives must be inside %s (set COFFER_DRIVE_ROOTS)", strings.Join(s.cfg.DriveRoots, ", "))
}

// pathWithin reports whether the clean absolute path p is dir or inside it
// (anything is inside "/").
func pathWithin(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// driveSetup is a validated drive config, as stored.
type driveSetup struct {
	conf string
	// newKey is a drive key Coffer just made, to show as a recovery key.
	newKey []byte
	// finish marks the bucket folder for the drive's key (or clears a stale
	// mark). Handlers call it after saving conf, so a marker never names a
	// key that Coffer failed to store.
	finish func(context.Context) error
}

// finishSetup runs setup.finish, which must not be cut short by the client
// going away. A failure is logged, not undone: the drive works without a
// marker, files still vouch for its key, and saving it again writes one.
// Undoing the save instead could throw away the only copy of a key that a
// marker written just before the error already names.
func (s *Server) finishSetup(ctx context.Context, setup driveSetup, name string) {
	if setup.finish == nil {
		return
	}
	if err := setup.finish(context.WithoutCancel(ctx)); err != nil {
		s.log.Warn("could not mark the bucket folder; save the drive again to retry", "drive", name, "err", err)
	}
}

// driveConfig validates the requested settings. It writes nothing lasting
// to the bucket: that is finish's job.
func (s *Server) driveConfig(ctx context.Context, req driveReq, kind string, existing *store.Drive) (driveSetup, error) {
	switch kind {
	case "local":
		p, err := s.localPath(req.Path)
		if err != nil {
			return driveSetup{}, err
		}
		b, _ := json.Marshal(localConfig{Path: p})
		return driveSetup{conf: string(b)}, nil
	case "s3":
		if req.S3 == nil {
			return driveSetup{}, errors.New("bucket settings are required")
		}
		c := *req.S3
		c.Key = nil
		var oldKey []byte
		if existing != nil {
			if pub := s3Public(existing); pub != nil && pub.Encrypt != c.Encrypt {
				return driveSetup{}, errors.New("encryption is chosen when a drive is added and can't be changed: add a new drive for an encrypted bucket folder")
			}
			if old, err := s.s3Config(existing); err == nil {
				if c.SecretKey == "" {
					c.SecretKey = old.SecretKey
				}
				oldKey = old.Key
			}
		}
		key := oldKey
		if c.Encrypt && strings.TrimSpace(req.RecoveryKey) != "" {
			k, err := files.ParseRecoveryKey(req.RecoveryKey)
			if err != nil {
				return driveSetup{}, err
			}
			key = k
		}
		// Check the bucket with a plain handle, then settle the drive key.
		probe := c
		probe.Encrypt = false
		d, err := files.OpenS3(probe)
		if err != nil {
			return driveSetup{}, err
		}
		if err := d.Check(ctx); err != nil {
			return driveSetup{}, fmt.Errorf("could not use the bucket: %w", err)
		}
		if c.Key, err = d.PrepareEncryption(ctx, c.Encrypt, key, false); err != nil {
			return driveSetup{}, err
		}
		final, err := files.OpenS3(c)
		if err != nil {
			return driveSetup{}, err
		}
		self := ""
		if existing != nil {
			self = existing.ID
		}
		if err := s.checkS3Overlap(final.Config(), self); err != nil {
			return driveSetup{}, err
		}
		st := s3Stored{S3Config: final.Config(), SecretSealed: s.keys.Encrypt([]byte(c.SecretKey))}
		st.SecretKey = ""
		setup := driveSetup{finish: func(ctx context.Context) error {
			_, err := d.PrepareEncryption(ctx, c.Encrypt, c.Key, true)
			return err
		}}
		if c.Encrypt {
			st.KeySealed = s.keys.Encrypt(c.Key)
			if key == nil {
				setup.newKey = c.Key
			}
		}
		b, _ := json.Marshal(st)
		setup.conf = string(b)
		return setup, nil
	}
	return driveSetup{}, errors.New("kind must be local or s3")
}

// checkS3Overlap refuses a bucket folder that is, contains or sits inside
// another drive's when one of them is encrypted and they don't share a key:
// the two would write plain and encrypted files, or two keys, side by side.
func (s *Server) checkS3Overlap(c files.S3Config, self string) error {
	prefix := strings.Trim(c.Prefix, "/")
	for _, m := range s.driveList() {
		if m.info.ID == self || m.info.Kind != "s3" {
			continue
		}
		o := s3Public(m.info)
		if o == nil || !strings.EqualFold(o.Endpoint, c.Endpoint) || o.Bucket != c.Bucket {
			continue
		}
		op := strings.Trim(o.Prefix, "/")
		nested := op == prefix || op == "" || prefix == "" || strings.HasPrefix(prefix+"/", op+"/") || strings.HasPrefix(op+"/", prefix+"/")
		if !nested || (!o.Encrypt && !c.Encrypt) {
			continue
		}
		if o.Encrypt == c.Encrypt {
			if oc, err := s.s3Config(m.info); err == nil && bytes.Equal(oc.Key, c.Key) {
				continue
			}
		}
		return fmt.Errorf("this bucket folder overlaps the drive %q, which uses different encryption: choose a separate folder", m.info.Name)
	}
	return nil
}

func (s *Server) handleDriveCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req driveReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil || !driveNameRe.MatchString(strings.TrimSpace(*req.Name)) {
		badRequest(w, "give the drive a name (letters, numbers and spaces, up to 48)")
		return
	}
	setup, err := s.driveConfig(r.Context(), req, req.Kind, nil)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	newKey := setup.newKey
	d := &store.Drive{ID: strings.ToLower(security.RandomToken(6)), Name: strings.TrimSpace(*req.Name), Kind: req.Kind, Config: setup.conf}
	if req.Sort != nil {
		d.Sort = *req.Sort
	}
	if err := s.store.CreateDrive(r.Context(), d); err != nil {
		s.fail(w, r, err)
		return
	}
	s.finishSetup(r.Context(), setup, d.Name)
	s.markLocal(d)
	if err := s.loadDrives(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	detail := d.Name + " (" + d.Kind + ")"
	if req.S3 != nil && req.S3.Encrypt {
		detail += ", encrypted"
	}
	s.audit(r, u, "", "drive.create", "", detail)
	m, _ := s.drive(d.ID)
	if m != nil && m.d != nil {
		go s.rescan(context.WithoutCancel(r.Context()), m)
	}
	out := s.driveOut(r.Context(), m)
	if newKey != nil {
		out.RecoveryKey = files.FormatRecoveryKey(newKey)
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleDriveUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req driveReq
	if !decodeJSON(w, r, &req) {
		return
	}
	m, err := s.drive(r.PathValue("id"))
	if m == nil {
		s.fail(w, r, err)
		return
	}
	d := *m.info
	if req.Name != nil {
		if !driveNameRe.MatchString(strings.TrimSpace(*req.Name)) {
			badRequest(w, "invalid drive name")
			return
		}
		d.Name = strings.TrimSpace(*req.Name)
	}
	if req.Sort != nil {
		d.Sort = *req.Sort
	}
	if req.Disabled != nil {
		d.Disabled = *req.Disabled
	}
	var setup driveSetup
	if (d.Kind == "local" && req.Path != "") || (d.Kind == "s3" && req.S3 != nil) {
		if setup, err = s.driveConfig(r.Context(), req, d.Kind, m.info); err != nil {
			badRequest(w, err.Error())
			return
		}
		d.Config = setup.conf
		_ = s.index.DropDrive(r.Context(), d.ID)
	}
	newKey := setup.newKey
	if err := s.store.UpdateDrive(r.Context(), &d); err != nil {
		s.fail(w, r, err)
		return
	}
	s.finishSetup(r.Context(), setup, d.Name)
	if d.Kind == "local" && req.Path != "" {
		s.markLocal(&d)
	}
	if err := s.loadDrives(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "drive.update", "", d.Name)
	nm, _ := s.drive(d.ID)
	out := s.driveOut(r.Context(), nm)
	if newKey != nil {
		out.RecoveryKey = files.FormatRecoveryKey(newKey)
	}
	writeJSON(w, http.StatusOK, out)
}

// markLocal marks the folder an admin picked for a local drive, so it is
// recognised (and an empty mount point isn't) from now on.
func (s *Server) markLocal(d *store.Drive) {
	if d.Kind != "local" {
		return
	}
	if err := files.MarkLocalDrive(s.localDir(d), d.ID); err != nil {
		s.log.Warn("could not mark the drive's folder", "drive", d.Name, "err", err)
	}
}

// handleDriveRecoveryKey shows an encrypted drive's recovery key again once
// the admin confirms their password. Without it, a bucket's files can't be
// read if this server and its secret key are lost.
func (s *Server) handleDriveRecoveryKey(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.checkPassword(w, r, u, req.Password) {
		return
	}
	m, err := s.drive(r.PathValue("id"))
	if m == nil {
		s.fail(w, r, err)
		return
	}
	if pub := s3Public(m.info); m.info.Kind != "s3" || pub == nil || !pub.Encrypt {
		badRequest(w, "this drive is not encrypted")
		return
	}
	c, err := s.s3Config(m.info)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	s.audit(r, u, "", "drive.recovery_key", "", m.info.Name)
	writeJSON(w, http.StatusOK, map[string]string{"recoveryKey": files.FormatRecoveryKey(c.Key)})
}

func (s *Server) handleDriveDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	m, err := s.drive(r.PathValue("id"))
	if m == nil {
		s.fail(w, r, err)
		return
	}
	if len(s.driveList()) <= 1 {
		badRequest(w, "keep at least one drive")
		return
	}
	if err := s.store.DeleteDrive(r.Context(), m.info.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.index.DropDrive(r.Context(), m.info.ID)
	if err := s.loadDrives(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "drive.delete", "", m.info.Name+" (files were not touched)")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDriveRescan(w http.ResponseWriter, r *http.Request, u *store.User) {
	// Look at the folder first, so a disk plugged back in comes online now.
	m, err := s.checkDrive(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.index.Scanning(m.info.ID) {
		writeErr(w, http.StatusConflict, "busy", "a scan is already running")
		return
	}
	go s.rescan(context.WithoutCancel(r.Context()), m)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDriveTest(w http.ResponseWriter, r *http.Request, _ *store.User) {
	var req driveReq
	if !decodeJSON(w, r, &req) {
		return
	}
	var existing *store.Drive
	if id := r.URL.Query().Get("id"); id != "" {
		if m, _ := s.drive(id); m != nil {
			existing = m.info
		}
	}
	if _, err := s.driveConfig(r.Context(), req, req.Kind, existing); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
