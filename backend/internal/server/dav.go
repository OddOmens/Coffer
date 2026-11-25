package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/webdav"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// WebDAV lets Finder (⌘K), Windows Explorer, Nautilus, rclone and others
// mount Coffer. Each place is a top-level folder. Clients sign in with the
// username and an app password, never the account password.

func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	s.mon.DavReqs.Add(1)
	if !s.cur().WebDAV {
		http.NotFound(w, r)
		return
	}
	// Responses may carry user files; never let them run as our origin.
	w.Header().Set("Content-Security-Policy", fileCSP)
	u, ok := s.davAuth(w, r)
	if !ok {
		return
	}
	ps, err := s.placesFor(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dfs := &davFS{s: s, u: u, r: r, byName: map[string]*access{}}
	for _, p := range ps {
		if !p.Online {
			continue
		}
		name := strings.NewReplacer("/", "-", "\\", "-").Replace(p.Name)
		for i := 2; dfs.byName[name] != nil; i++ {
			name = fmt.Sprintf("%s (%d)", strings.NewReplacer("/", "-").Replace(p.Name), i)
		}
		dfs.byName[name] = p
		dfs.order = append(dfs.order, name)
	}
	switch r.Method {
	case "PROPFIND":
		// Depth: infinity would walk whole drives in one request (RFC 4918
		// lets servers refuse it). A missing Depth means infinity too.
		switch strings.TrimSpace(r.Header.Get("Depth")) {
		case "":
			r.Header.Set("Depth", "1")
		case "infinity":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>`)
			return
		}
	case "LOCK":
		r.Header.Set("Timeout", capLockTimeout(r.Header.Get("Timeout")))
	case http.MethodGet, http.MethodHead:
		// Anything but media goes out as plain bytes, as in serveFile, so an
		// uploaded .js can't load as a script on our origin with the
		// browser's cached Basic credentials. ServeContent keeps a type
		// that is already set. SVGs are images, so the sandbox still applies.
		w.Header().Set("Content-Security-Policy", fileCSP)
		if ct := files.MimeType(path.Base(r.URL.Path)); strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
	}
	if r.Method != http.MethodPut {
		// Only PUT carries file content; XML bodies are small.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	} else if room := s.davTmpRoom(); room >= 0 && r.ContentLength > room {
		// Uploads are buffered on Coffer's own disk first; don't fill it.
		writeErr(w, http.StatusInsufficientStorage, "no_space", "not enough free space on the server to take this upload")
		return
	}
	h := &webdav.Handler{
		Prefix: "/dav", FileSystem: dfs, LockSystem: s.davLocksFor(u.ID),
		Logger: func(r *http.Request, err error) {
			if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, os.ErrExist) {
				s.log.Debug("webdav", "method", r.Method, "err", err)
			}
		},
	}
	h.ServeHTTP(w, r)
}

// davLockSets holds a WebDAV lock system per user. Each user's DAV paths are
// named after their own places, so one shared lock system let anyone lock
// "/" (or a place name another user also has) and block everyone's writes.
// Coffer's own locks still apply across users.
var davLockSets sync.Map // davLockKey -> webdav.LockSystem

type davLockKey struct {
	s   *Server
	uid int64
}

func (s *Server) davLocksFor(uid int64) webdav.LockSystem {
	ls, _ := davLockSets.LoadOrStore(davLockKey{s, uid}, webdav.NewMemLS())
	return ls.(webdav.LockSystem)
}

// maxDavLock caps WebDAV lock timeouts (seconds); clients refresh their
// locks, and one that vanishes can't hold a lock forever.
const maxDavLock = 3600

func capLockTimeout(h string) string {
	first, _, _ := strings.Cut(h, ",")
	if n, ok := strings.CutPrefix(strings.TrimSpace(first), "Second-"); ok {
		if v, err := strconv.ParseInt(n, 10, 64); err == nil && v >= 0 && v <= maxDavLock {
			return h
		}
	}
	return fmt.Sprintf("Second-%d", maxDavLock)
}

func (s *Server) davAuth(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	deny := func() (*store.User, bool) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Coffer", charset="UTF-8"`)
		writeErr(w, http.StatusUnauthorized, "unauthenticated", "sign in with your username and an app password")
		return nil, false
	}
	key := "dav:" + ipString(r)
	if wait := s.backoff.Wait(key); wait > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "locked", "too many failed sign-ins")
		return nil, false
	}
	name, secret, ok := r.BasicAuth()
	if !ok || secret == "" {
		return deny()
	}
	t, err := s.store.AppTokenByHash(r.Context(), security.HashToken(secret))
	var u *store.User
	if err == nil {
		u, err = s.store.UserByID(r.Context(), t.UserID)
	}
	if err != nil || u.Disabled || subtle.ConstantTimeCompare([]byte(strings.ToLower(u.Username)), []byte(strings.ToLower(name))) != 1 || s.mustEnroll(u) {
		s.backoff.Fail(key)
		if err == nil {
			s.audit(r, nil, "", "webdav.denied", "", "username="+truncate(name, 64))
		}
		return deny()
	}
	s.backoff.Reset(key)
	if time.Now().Unix()-t.LastUsed > 300 || t.LastIP != ipString(r) {
		_ = s.store.TouchAppToken(r.Context(), t.ID, ipString(r))
	}
	return u, true
}

type davFS struct {
	s      *Server
	u      *store.User
	r      *http.Request
	byName map[string]*access
	order  []string
}

var errDavRoot = errors.New("not allowed at the top level")

// split maps "/Place/a/b" to the place and its drive-relative path.
func (f *davFS) split(name string) (*access, string, error) {
	name = strings.Trim(path.Clean("/"+name), "/")
	if name == "" {
		return nil, "", nil
	}
	top, rest, _ := strings.Cut(name, "/")
	a := f.byName[top]
	if a == nil {
		return nil, "", fs.ErrNotExist
	}
	p, err := a.resolve(rest)
	if err != nil {
		return nil, "", fs.ErrNotExist
	}
	return a, p, nil
}

type davInfo struct {
	name  string
	e     files.Entry
	isDir bool
}

func (i davInfo) Name() string { return i.name }
func (i davInfo) Size() int64 {
	if i.isDir {
		return 0
	}
	return i.e.Size
}
func (i davInfo) Mode() fs.FileMode {
	if i.isDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i davInfo) ModTime() time.Time { return time.Unix(i.e.ModTime, 0) }
func (i davInfo) IsDir() bool        { return i.isDir }
func (i davInfo) Sys() any           { return nil }

// ContentType comes from the extension, like everywhere else: without it
// PROPFIND opens and sniffs every file it lists.
func (i davInfo) ContentType(context.Context) (string, error) { return files.MimeType(i.name), nil }

func info(name string, e files.Entry) davInfo { return davInfo{name: name, e: e, isDir: e.IsDir} }

func (f *davFS) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	a, p, err := f.split(name)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return davInfo{name: "/", isDir: true}, nil
	}
	e, err := a.d().Stat(ctx, p)
	if err != nil {
		return nil, err
	}
	n := e.Name
	if p == a.Root {
		n = path.Base(strings.Trim(path.Clean("/"+name), "/"))
	}
	return info(n, e), nil
}

func (f *davFS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	a, p, err := f.split(name)
	if err != nil {
		return err
	}
	if a == nil || p == a.Root {
		return errDavRoot
	}
	if !a.CanWrite || files.ValidName(path.Base(p)) != nil {
		return fs.ErrPermission
	}
	if err := a.d().Mkdir(ctx, p); err != nil {
		if errors.Is(err, files.ErrExists) {
			return os.ErrExist
		}
		return err
	}
	f.s.reindex(a.m, files.Parent(p))
	return nil
}

func (f *davFS) RemoveAll(ctx context.Context, name string) error {
	a, p, err := f.split(name)
	if err != nil {
		return err
	}
	if a == nil || p == a.Root {
		return errDavRoot
	}
	if !a.CanDelete || f.s.lockedErr(ctx, f.u, a.DriveID, p) != nil {
		return fs.ErrPermission
	}
	// Deletes from desktop clients go to the trash, like the web app.
	return f.s.trash(f.r, f.u, a.m, p)
}

func (f *davFS) Rename(ctx context.Context, oldName, newName string) error {
	a, src, err := f.split(oldName)
	if err != nil {
		return err
	}
	b, dst, err := f.split(newName)
	if err != nil {
		return err
	}
	if a == nil || b == nil || src == a.Root || dst == b.Root {
		return errDavRoot
	}
	if !a.CanWrite || !b.CanWrite || f.s.lockedErr(ctx, f.u, a.DriveID, src) != nil || files.ValidName(path.Base(dst)) != nil {
		return fs.ErrPermission
	}
	// As in the web app: leaving a place needs delete rights there, and
	// the destination's storage limit applies.
	if !a.CanDelete && (a.DriveID != b.DriveID || !files.Within(a.Root, dst)) {
		return fs.ErrPermission
	}
	if a.DriveID == b.DriveID {
		if f.s.quotaCheckMove(ctx, b.DriveID, src, files.Parent(dst), f.s.sizeOf(ctx, a.m, src)) != nil {
			return fs.ErrPermission
		}
	} else if f.s.quotaCheck(ctx, b.DriveID, files.Parent(dst), f.s.sizeOf(ctx, a.m, src)) != nil {
		return fs.ErrPermission
	}
	if a.DriveID == b.DriveID {
		if err := a.d().Rename(ctx, src, dst); err != nil {
			if errors.Is(err, files.ErrExists) {
				return os.ErrExist
			}
			return err
		}
		f.s.afterMove(a.m, src, dst)
		f.s.audit(f.r, f.u, "", "move", a.DriveID+":"+src, "to "+dst+" (WebDAV)")
		return nil
	}
	if err := copyAcross(ctx, a.d(), src, b.d(), dst); err != nil {
		return err
	}
	f.s.reindex(b.m, files.Parent(dst))
	return f.s.trash(f.r, f.u, a.m, src)
}

func (f *davFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	a, p, err := f.split(name)
	if err != nil {
		return nil, err
	}
	// O_RDWR alone is PROPPATCH asking for dead properties, not a write:
	// treating it as one replaced the file with an empty upload on Close.
	writing := flag&(os.O_WRONLY|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0
	if a == nil {
		if writing {
			return nil, errDavRoot
		}
		var list []fs.FileInfo
		for _, n := range f.order {
			list = append(list, davInfo{name: n, isDir: true})
		}
		return &davDir{info: davInfo{name: "/", isDir: true}, list: list}, nil
	}
	if writing {
		if p == a.Root {
			return nil, errDavRoot
		}
		if !a.CanWrite || f.s.lockedErr(ctx, f.u, a.DriveID, p) != nil {
			return nil, fs.ErrPermission
		}
		if e, err := a.d().Stat(ctx, p); err == nil && e.IsDir {
			return nil, files.ErrIsDir
		} else if err != nil && flag&os.O_CREATE == 0 {
			return nil, fs.ErrNotExist
		}
		// Saving over a file replaces it, as a web upload does: that
		// needs delete rights, and the old version goes to the trash.
		if _, ok := replaces(ctx, a.d(), p); ok && !a.CanDelete {
			return nil, fs.ErrPermission
		}
		if err := files.ValidName(path.Base(p)); err != nil {
			return nil, fs.ErrPermission
		}
		dir := filepath.Join(f.s.cfg.DataDir, "dav-tmp")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// Stop buffering once the upload can't fit: the storage limit, or
		// the drive's free space.
		limit := f.s.quotaLeft(ctx, a.DriveID, files.Parent(p))
		if free, _, ok := a.d().Usage(ctx); ok && (limit < 0 || free < limit) {
			limit = max(0, free)
		}
		if room := f.s.davTmpRoom(); room >= 0 && (limit < 0 || room < limit) {
			limit = room
		}
		tmp, err := os.CreateTemp(dir, "put-*")
		if err != nil {
			return nil, err
		}
		return &davWriter{File: tmp, f: f, a: a, p: p, limit: limit}, nil
	}
	e, err := a.d().Stat(ctx, p)
	if err != nil {
		return nil, err
	}
	n := e.Name
	if p == a.Root {
		n = path.Base(strings.Trim(path.Clean("/"+name), "/"))
	}
	if e.IsDir {
		es, err := a.d().List(ctx, p, false)
		if err != nil {
			return nil, err
		}
		es = a.visible(es)
		list := make([]fs.FileInfo, len(es))
		for i, c := range es {
			list[i] = info(c.Name, c)
		}
		return &davDir{info: info(n, e), list: list}, nil
	}
	rc, e, err := a.d().Open(ctx, p)
	if err != nil {
		return nil, err
	}
	return &davFile{ReadSeekCloser: rc, info: info(n, e)}, nil
}

type davFile struct {
	io.ReadSeekCloser
	info davInfo
}

func (f *davFile) Readdir(int) ([]fs.FileInfo, error) { return nil, files.ErrNotDir }
func (f *davFile) Stat() (fs.FileInfo, error)         { return f.info, nil }
func (f *davFile) Write([]byte) (int, error)          { return 0, fs.ErrPermission }

type davDir struct {
	info davInfo
	list []fs.FileInfo
	pos  int
}

func (d *davDir) Close() error                   { return nil }
func (d *davDir) Read([]byte) (int, error)       { return 0, files.ErrIsDir }
func (d *davDir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *davDir) Write([]byte) (int, error)      { return 0, fs.ErrPermission }
func (d *davDir) Stat() (fs.FileInfo, error)     { return d.info, nil }
func (d *davDir) Readdir(count int) ([]fs.FileInfo, error) {
	if count <= 0 {
		rest := d.list[d.pos:]
		d.pos = len(d.list)
		return rest, nil
	}
	if d.pos >= len(d.list) {
		return nil, io.EOF
	}
	end := min(d.pos+count, len(d.list))
	out := d.list[d.pos:end]
	d.pos = end
	return out, nil
}

// davTmpRoom returns how many bytes WebDAV uploads may still buffer on
// Coffer's data disk, keeping max(2 GB, 5%) of it free, or -1 when unknown.
func (s *Server) davTmpRoom() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.DataDir, &st); err != nil {
		return -1
	}
	free, total := int64(st.Bavail)*st.Bsize, int64(st.Blocks)*st.Bsize
	return max(0, free-max(2<<30, total/20))
}

// davWriter buffers an upload in a temp file and stores it on Close, so a
// half-finished PUT never replaces a good file.
type davWriter struct {
	*os.File
	f       *davFS
	a       *access
	p       string
	closed  bool
	limit   int64 // bytes that may be written, or -1
	n       int64
	checked int64 // n when the data disk's free space was last looked at
	err     error // a failed or cut-off upload: Close discards it
}

var errDavFull = errors.New("upload exceeds the storage limit or free space")

// davCheckEvery is how often (in bytes) a WebDAV upload looks at the data
// disk again: uploads side by side share its free space.
const davCheckEvery = 64 << 20

func (w *davWriter) roomLeft() error {
	if w.n-w.checked < davCheckEvery {
		return nil
	}
	w.checked = w.n
	if w.f.s.davTmpRoom() == 0 {
		return errDavFull
	}
	return nil
}

func (w *davWriter) Readdir(int) ([]fs.FileInfo, error) { return nil, files.ErrNotDir }

func (w *davWriter) Write(b []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.limit >= 0 && w.n+int64(len(b)) > w.limit {
		w.err = errDavFull
		return 0, w.err
	}
	n, err := w.File.Write(b)
	w.n += int64(n)
	if err == nil {
		err = w.roomLeft()
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

// ReadFrom is what io.Copy uses for PUT bodies. It records a body that ends
// early (a dropped connection), which *os.File's own ReadFrom would hide.
func (w *davWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.err != nil {
		return 0, w.err
	}
	// In steps, looking at the data disk's free space in between.
	var total int64
	for {
		step := int64(davCheckEvery)
		if w.limit >= 0 {
			step = min(step, w.limit-w.n+1)
		}
		n, err := w.File.ReadFrom(io.LimitReader(r, step))
		w.n += n
		total += n
		if err == nil && w.limit >= 0 && w.n > w.limit {
			err = errDavFull
		}
		if err == nil && n < step {
			return total, nil // the end of the body
		}
		if err == nil {
			err = w.roomLeft()
		}
		if err != nil {
			w.err = err
			return total, err
		}
	}
}

func (w *davWriter) Stat() (fs.FileInfo, error) {
	st, err := w.File.Stat()
	if err != nil {
		return nil, err
	}
	return davInfo{name: path.Base(w.p), e: files.Entry{Size: st.Size(), ModTime: time.Now().Unix()}}, nil
}

func (w *davWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer os.Remove(w.File.Name())
	if w.err != nil {
		w.File.Close()
		return w.err
	}
	size, err := w.File.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = w.File.Seek(0, io.SeekStart)
	}
	if err != nil {
		w.File.Close()
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	if err := w.f.s.quotaCheck(ctx, w.a.DriveID, files.Parent(w.p), size); err != nil {
		w.File.Close()
		return fs.ErrPermission
	}
	if _, ok := replaces(ctx, w.a.d(), w.p); ok && !w.a.CanDelete {
		w.File.Close()
		return fs.ErrPermission
	}
	err = w.f.s.replaceFile(ctx, w.f.u, w.a.m, w.p, w.File, size)
	w.File.Close()
	if err != nil {
		return err
	}
	w.f.s.mon.BytesIn.Add(size)
	w.f.s.reindex(w.a.m, files.Parent(w.p))
	go w.f.s.queueMedia(w.a.m, w.p)
	if !strings.HasPrefix(path.Base(w.p), "._") && path.Base(w.p) != ".DS_Store" {
		w.f.s.audit(w.f.r, w.f.u, "", "upload", w.a.DriveID+":"+w.p, fmt.Sprintf("%d bytes (WebDAV)", size))
	}
	return nil
}
