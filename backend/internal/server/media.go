package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// Generated previews. The media worker (the coffer-media container, or this
// process when ffmpeg is installed) turns videos into H.264 MP4 proxies and
// takes JPEG stills of HEIC/RAW photos and video frames. Renditions live in
// DATA/media, keyed by drive, path, size and modification time, so a changed
// file gets new ones.

func (s *Server) mediaDir() string { return filepath.Join(s.cfg.DataDir, "media") }

func (s *Server) mediaPath(drive, p string, size, mtime int64, kind string) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", drive, p, size, mtime))
	k := hex.EncodeToString(sum[:])
	ext := ".mp4"
	if kind == store.MediaStill {
		ext = ".jpg"
	}
	return filepath.Join(s.mediaDir(), k[:2], k+ext)
}

func mediaKindOK(kind, p string) bool {
	switch kind {
	case store.MediaProxy:
		return files.IsVideo(p)
	case store.MediaStill:
		return files.NeedsStill(p)
	}
	return false
}

// workerOnline reports whether a media worker checked in recently.
func (s *Server) workerOnline(ctx context.Context) bool {
	v, err := s.store.KV(ctx, "media_worker")
	if err != nil {
		return false
	}
	t, _ := strconv.ParseInt(v, 10, 64)
	return time.Since(time.Unix(t, 0)) < 90*time.Second
}

// mediaFor returns the rendition's file when it is ready. Otherwise it
// queues it (when enqueue is set) and returns the job.
func (s *Server) mediaFor(ctx context.Context, m *mounted, p, kind string, enqueue bool) (string, *store.MediaJob, error) {
	e, err := m.d.Stat(ctx, p)
	if err != nil {
		return "", nil, err
	}
	if e.IsDir {
		return "", nil, fs.ErrNotExist
	}
	f := s.mediaPath(m.info.ID, p, e.Size, e.ModTime, kind)
	if info, err := os.Stat(f); err == nil {
		touchMedia(f, info)
		return f, nil, nil
	}
	if !enqueue {
		j, err := s.store.MediaJob(ctx, m.info.ID, p, kind)
		if errors.Is(err, store.ErrNotFound) {
			return "", nil, nil
		}
		return "", j, err
	}
	j, err := s.store.EnqueueMedia(ctx, m.info.ID, p, kind, e.Size, e.ModTime)
	return "", j, err
}

// queueMedia prepares previews for a newly written file, when the admin
// wants them made ahead of time.
func (s *Server) queueMedia(m *mounted, p string) {
	if !s.cur().MediaAuto {
		return
	}
	ctx := context.Background()
	e, err := m.d.Stat(ctx, p)
	if err != nil || e.IsDir {
		return
	}
	if files.NeedsStill(p) {
		_, _ = s.store.EnqueueMedia(ctx, m.info.ID, p, store.MediaStill, e.Size, e.ModTime)
	}
	if files.NeedsProxy(p) {
		_, _ = s.store.EnqueueMedia(ctx, m.info.ID, p, store.MediaProxy, e.Size, e.ModTime)
	}
}

// serveMedia sends a rendition, or reports its progress with 202.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, m *mounted, p string) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if !mediaKindOK(kind, p) {
		writeErr(w, http.StatusNotFound, "no_media", "no preview for this file")
		return
	}
	if q.Get("retry") == "1" {
		_ = s.store.ResetMedia(r.Context(), m.info.ID, p, kind)
	}
	file, job, err := s.mediaFor(r.Context(), m, p, kind, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if file == "" || q.Get("status") == "1" {
		state := "ready"
		out := map[string]any{}
		if file == "" {
			state = "queued"
			if job != nil {
				state = job.State
				out["error"] = job.Error
			}
			if state != "failed" && !s.workerOnline(r.Context()) {
				state = "unavailable"
			}
		}
		out["state"] = state
		status := http.StatusOK
		if file == "" {
			status = http.StatusAccepted
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, status, out)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	info, _ := f.Stat()
	ctype, name := "video/mp4", strings.TrimSuffix(path.Base(p), path.Ext(p))+" (preview).mp4"
	if kind == store.MediaStill {
		ctype, name = "image/jpeg", strings.TrimSuffix(path.Base(p), path.Ext(p))+".jpg"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	if v := mime.FormatMediaType("inline", map[string]string{"filename": name}); v != "" {
		h.Set("Content-Disposition", v)
	}
	h.Set("Content-Security-Policy", fileCSP)
	h.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// serveThumbFor sends a small thumbnail: rendered directly for common image
// formats, or from the generated still for HEIC, RAW and video.
func (s *Server) serveThumbFor(w http.ResponseWriter, r *http.Request, m *mounted, p string) {
	if files.Thumbable(p) {
		if tp, err := s.thumbs.Get(r.Context(), m.info.ID, m.d, p); err == nil {
			serveThumb(w, r, tp)
			return
		}
	}
	if files.NeedsStill(p) {
		if file, _, err := s.mediaFor(r.Context(), m, p, store.MediaStill, true); err == nil && file != "" {
			if tp, err := s.thumbs.FromFile(path.Base(file), file); err == nil {
				serveThumb(w, r, tp)
				return
			}
		}
	}
	writeErr(w, http.StatusNotFound, "no_thumb", "no thumbnail")
}

// Watermarked previews for links that ask for them.

var wmSlots = make(chan struct{}, 2)

// sendWatermarked serves the watermarked preview cached under key (what it
// is made from, and the text), rendering it the first time.
func (s *Server) sendWatermarked(w http.ResponseWriter, r *http.Request, key string, render func() ([]byte, error)) {
	file, err := s.thumbs.Watermarked(key, func() ([]byte, error) {
		select {
		case wmSlots <- struct{}{}:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		defer func() { <-wmSlots }()
		return render()
	})
	if err != nil {
		if r.Context().Err() == nil {
			writeErr(w, http.StatusNotFound, "no_preview", "no preview for this file")
		}
		return
	}
	f, err := os.Open(file)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no_preview", "no preview for this file")
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Security-Policy", fileCSP)
	h.Set("Cache-Control", "private, no-store")
	if info, err := f.Stat(); err == nil {
		h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, f)
	}
}

func (s *Server) serveWatermarked(w http.ResponseWriter, r *http.Request, m *mounted, p, text string) {
	e, err := m.d.Stat(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", m.info.ID, p, e.Size, e.ModTime, text)
	s.sendWatermarked(w, r, key, func() ([]byte, error) { return files.WatermarkJPEG(r.Context(), m.d, p, text) })
}

func (s *Server) serveWatermarkedStill(w http.ResponseWriter, r *http.Request, m *mounted, p, text string) {
	if !mediaKindOK(store.MediaStill, p) {
		writeErr(w, http.StatusNotFound, "no_media", "no preview for this file")
		return
	}
	file, job, err := s.mediaFor(r.Context(), m, p, store.MediaStill, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if file == "" {
		state := "queued"
		if job != nil {
			state = job.State
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"state": state})
		return
	}
	s.sendWatermarked(w, r, "file\x00"+file+"\x00"+text, func() ([]byte, error) { return files.WatermarkFile(file, text) })
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.serveMedia(w, r, a.m, p)
}

// The worker.

// MediaToolsInstalled reports whether ffmpeg is on PATH.
func MediaToolsInstalled() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// RunMediaWorker processes queued renditions until ctx is cancelled.
func (s *Server) RunMediaWorker(ctx context.Context) {
	_, magickErr := exec.LookPath("magick")
	s.log.Info("media worker started", "imagemagick", magickErr == nil)
	beat := func() {
		_ = s.store.PutKV(context.Background(), "media_worker", strconv.FormatInt(time.Now().Unix(), 10))
	}
	beat()
	// Keep checking in during renders that take hours, so health checks can
	// tell a busy worker from a stopped one.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				beat()
			}
		}
	}()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	reload := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			beat()
		default:
		}
		// Pick up drives added or changed in the main process.
		if time.Since(reload) > 2*time.Minute {
			if st, err := s.store.Settings(ctx); err == nil {
				s.settings.Store(&st)
			}
			_ = s.loadDrives(ctx)
			reload = time.Now()
		}
		j, err := s.store.ClaimMedia(ctx, time.Now().Add(-6*time.Hour).Unix())
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("media queue", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				beat()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if j.Attempts > 3 {
			_ = s.store.FinishMedia(ctx, j, "failed", "gave up after 3 attempts")
			continue
		}
		start := time.Now()
		state, msg := "done", ""
		if err := s.renderMedia(ctx, j, magickErr == nil); err != nil {
			state, msg = "failed", truncate(err.Error(), 300)
			s.log.Warn("media job failed", "drive", j.DriveID, "path", j.Path, "kind", j.Kind, "err", err)
		} else {
			s.log.Info("media job done", "drive", j.DriveID, "path", j.Path, "kind", j.Kind, "took", time.Since(start).Round(time.Second))
		}
		if ctx.Err() != nil {
			return
		}
		_ = s.store.FinishMedia(context.Background(), j, state, msg)
	}
}

// inputFormat forces ffmpeg's demuxer from the extension, so a file can't
// pretend to be a playlist that pulls in other files.
var inputFormat = map[string]string{
	".mov": "mov", ".mp4": "mov", ".m4v": "mov", ".3gp": "mov", ".mkv": "matroska", ".webm": "matroska",
	".avi": "avi", ".mxf": "mxf", ".mts": "mpegts", ".m2ts": "mpegts", ".wmv": "asf", ".flv": "flv",
	".mpg": "mpeg", ".mpeg": "mpeg",
}

func (s *Server) renderMedia(ctx context.Context, j *store.MediaJob, haveMagick bool) error {
	m, err := s.drive(j.DriveID)
	if err != nil {
		return err
	}
	e, err := m.d.Stat(ctx, j.Path)
	if err != nil {
		return err
	}
	if e.Size != j.Size || e.ModTime != j.MTime {
		// Changed since it was queued; queue the new version instead.
		_, err := s.store.EnqueueMedia(ctx, j.DriveID, j.Path, j.Kind, e.Size, e.ModTime)
		if err == nil {
			err = errors.New("file changed; queued again")
		}
		return err
	}
	tmpDir := filepath.Join(s.mediaDir(), "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}
	// Local files are handed to the tool as an open descriptor, so it reads
	// through the same confined handle; bucket objects are copied first.
	rc, _, err := m.d.Open(ctx, j.Path)
	if err != nil {
		return err
	}
	defer rc.Close()
	src, ok := rc.(*os.File)
	if !ok {
		tf, err := os.CreateTemp(tmpDir, "src-*")
		if err != nil {
			return err
		}
		defer os.Remove(tf.Name())
		defer tf.Close()
		if _, err := io.Copy(tf, rc); err != nil {
			return err
		}
		if _, err := tf.Seek(0, 0); err != nil {
			return err
		}
		src = tf
	}
	final := s.mediaPath(j.DriveID, j.Path, j.Size, j.MTime, j.Kind)
	out := filepath.Join(tmpDir, security.RandomToken(9)+filepath.Ext(final))
	defer os.Remove(out)

	ext := strings.ToLower(path.Ext(j.Path))
	if files.IsVideo(j.Path) && inputFormat[ext] == "" {
		// Never let ffmpeg probe: hls and concat playlists read other files.
		return fmt.Errorf("no demuxer set for %s files", ext)
	}
	in := "/dev/fd/3"
	switch {
	case j.Kind == store.MediaProxy:
		err = runTool(ctx, 8*time.Hour, src, "ffmpeg", s.proxyArgs(ext, in, out)...)
	case files.IsVideo(j.Path):
		err = runTool(ctx, 5*time.Minute, src, "ffmpeg", posterArgs(ext, in, out, "1")...)
		if err != nil || fileEmpty(out) {
			// Shorter than a second.
			if _, err := src.Seek(0, 0); err != nil {
				return err
			}
			err = runTool(ctx, 5*time.Minute, src, "ffmpeg", posterArgs(ext, in, out, "0")...)
		}
	default:
		if !haveMagick {
			return errors.New("ImageMagick is not installed in the media worker")
		}
		err = runTool(ctx, 5*time.Minute, src, "magick",
			"-limit", "memory", "1GiB", "-limit", "map", "2GiB", "-limit", "disk", "4GiB", "-limit", "time", "240",
			magickCoder(ext)+":"+in+"[0]", "-auto-orient", "-resize", "2560x2560>", "-strip", "-quality", "85", "JPEG:"+out)
	}
	if err != nil {
		return err
	}
	if fileEmpty(out) {
		return errors.New("the tool produced no output")
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	return os.Rename(out, final)
}

func (s *Server) proxyArgs(ext, in, out string) []string {
	h := s.cur().ProxyHeight
	if h != 720 {
		h = 1080
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file"}
	if f := inputFormat[ext]; f != "" {
		args = append(args, "-f", f)
	}
	args = append(args, "-i", in,
		"-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)':flags=bicubic,format=yuv420p", h),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-profile:v", "high",
		"-c:a", "aac", "-b:a", "160k", "-ac", "2",
		"-movflags", "+faststart", "-f", "mp4")
	if t := os.Getenv("COFFER_MEDIA_THREADS"); t != "" {
		args = append(args, "-threads", t)
	}
	return append(args, out)
}

func posterArgs(ext, in, out, at string) []string {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file", "-ss", at}
	if f := inputFormat[ext]; f != "" {
		args = append(args, "-f", f)
	}
	return append(args, "-i", in, "-frames:v", "1", "-vf", "scale='min(1920,iw)':-2", "-q:v", "3", "-f", "image2", "-c:v", "mjpeg", out)
}

// magickCoder names ImageMagick's reader for a photo extension, so the file
// is never sniffed. The media image's policy.xml allows only these.
func magickCoder(ext string) string {
	coder := strings.ToUpper(strings.TrimPrefix(ext, "."))
	if coder == "TIF" {
		coder = "TIFF"
	}
	return coder
}

func fileEmpty(p string) bool {
	info, err := os.Stat(p)
	return err != nil || info.Size() == 0
}

func runTool(ctx context.Context, limit time.Duration, src *os.File, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.ExtraFiles = []*os.File{src}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/tmp", "MAGICK_TMPDIR=/tmp"}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s took longer than %s", name, limit)
		}
		return fmt.Errorf("%s: %v: %s", name, err, msg)
	}
	return nil
}

// cleanMediaTmp removes leftovers of renders interrupted by a restart.
func (s *Server) cleanMediaTmp() {
	dir := filepath.Join(s.mediaDir(), "tmp")
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range es {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 12*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
