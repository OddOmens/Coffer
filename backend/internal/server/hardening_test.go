package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/oddomens/coffer/internal/config"
	"github.com/oddomens/coffer/internal/files"
)

func TestLocalPathRefusesSystemAndDataFolders(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "srv", "data")
	ok := filepath.Join(root, "srv", "projects")
	os.MkdirAll(data, 0o700)
	os.MkdirAll(ok, 0o755)
	s := &Server{cfg: &config.Config{DataDir: data, DriveRoots: []string{"/"}}}
	for _, p := range []string{"/", "/proc", "/proc/self", "/sys", "/dev", data, filepath.Dir(data), root} {
		if _, err := s.localPath(p); err == nil {
			t.Errorf("%s was accepted as a drive", p)
		}
	}
	if _, err := s.localPath(ok); err != nil {
		t.Errorf("%s: %v", ok, err)
	}
	// A data folder that doesn't exist yet is still protected.
	s.cfg.DataDir = filepath.Join(ok, "later")
	if _, err := s.localPath(ok); err == nil {
		t.Error("a drive around a not-yet-created data folder was accepted")
	}
}

// Forced downloads must not carry a type the browser would run as a script
// or apply as a stylesheet when a page on our origin includes them.
func TestDownloadsAreNotActiveContent(t *testing.T) {
	for name, want := range map[string]string{
		"evil.js": "application/octet-stream", "evil.mjs": "application/octet-stream", "x.css": "application/octet-stream",
		"x.html": "application/octet-stream", "x.xsl": "application/octet-stream", "x.json": "application/octet-stream",
		"clip.mkv": "video/x-matroska", "photo.png": "image/png",
	} {
		w := httptest.NewRecorder()
		serveFile(w, httptest.NewRequest("GET", "/", nil), strings.NewReader("x"), files.Entry{}, name, false)
		if got := w.Header().Get("Content-Type"); got != want {
			t.Errorf("%s: Content-Type %q, want %q", name, got, want)
		}
	}
	w := httptest.NewRecorder()
	serveFile(w, httptest.NewRequest("GET", "/", nil), strings.NewReader("x"), files.Entry{}, "app.js", true)
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("inline preview of .js: %q", got)
	}
}

// ffmpeg must never probe the input format: the hls and concat demuxers
// would follow a playlist to other files.
func TestEveryVideoHasAFixedDemuxer(t *testing.T) {
	for _, ext := range []string{".mov", ".mp4", ".m4v", ".3gp", ".mkv", ".webm", ".avi", ".mxf", ".mts", ".m2ts", ".wmv",
		".flv", ".mpg", ".mpeg", ".m3u8", ".ts", ".ogv", ".r3d", ".braw", ".vob", ".txt", ".ffconcat"} {
		if files.IsVideo("x"+ext) && inputFormat[ext] == "" {
			t.Errorf("%s is a video but has no fixed demuxer", ext)
		}
	}
	joined := strings.Join(posterArgs(".mov", "/dev/fd/3", "out.jpg", "1"), " ")
	if !strings.Contains(joined, "-protocol_whitelist file") || !strings.Contains(joined, "-f mov -i /dev/fd/3") {
		t.Errorf("unsafe ffmpeg arguments: %s", joined)
	}
}

// The media image's ImageMagick policy must allow reading every still format
// Coffer asks for (and nothing is read by sniffing).
func TestImageMagickPolicyCoversStills(t *testing.T) {
	b, err := os.ReadFile("../../../Dockerfile")
	if err != nil {
		t.Skip("Dockerfile not available")
	}
	m := regexp.MustCompile(`domain="coder" rights="read" pattern="\{([A-Z0-9,]+)\}"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no coder allow-list in the media image's policy")
	}
	allowed := map[string]bool{}
	for _, c := range strings.Split(string(m[1]), ",") {
		allowed[c] = true
	}
	for _, ext := range []string{".heic", ".heif", ".avif", ".tif", ".tiff", ".bmp", ".psd", ".dng", ".cr2", ".cr3", ".nef",
		".arw", ".raf", ".orf", ".rw2", ".pef", ".srw", ".3fr", ".raw"} {
		if !files.NeedsStill("x"+ext) || files.IsVideo("x"+ext) {
			continue
		}
		if c := magickCoder(ext); !allowed[c] {
			t.Errorf("%s: coder %s is not allowed by the policy", ext, c)
		}
	}
}
