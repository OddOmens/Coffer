package server

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/files"
)

// inlineTypes may be shown in the browser. Everything else is forced to
// download, so a stored HTML or SVG file can never run script on our origin.
var inlineTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true,
	"image/svg+xml": true, // safe only because of the sandbox CSP below
	"video/mp4":     true, "video/webm": true, "video/quicktime": true,
	"audio/mpeg": true, "audio/wav": true, "audio/ogg": true, "audio/flac": true, "audio/mp4": true, "audio/aac": true,
	"application/pdf": true, "text/plain": true,
}

// textPreview lists extensions previewed as plain text.
var textPreview = map[string]bool{
	".txt": true, ".md": true, ".csv": true, ".json": true, ".log": true, ".srt": true, ".vtt": true,
	".xml": true, ".yml": true, ".yaml": true, ".toml": true, ".ini": true, ".js": true, ".jsx": true,
	".ts": true, ".css": true, ".html": true, ".htm": true,
}

// fileCSP sandboxes file responses even if a browser is navigated to them.
const fileCSP = "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'"

func serveFile(w http.ResponseWriter, r *http.Request, f io.ReadSeeker, info files.Entry, name string, inline bool) {
	ctype := files.MimeType(name)
	if inline && textPreview[strings.ToLower(path.Ext(name))] {
		ctype = "text/plain"
	}
	if !inlineTypes[ctype] {
		inline = false
	}
	if !inline && !strings.HasPrefix(ctype, "image/") && !strings.HasPrefix(ctype, "video/") && !strings.HasPrefix(ctype, "audio/") {
		// Downloads are just bytes. With nosniff this stops an uploaded .js or
		// .css from loading as a script or stylesheet on our origin, which the
		// app's CSP ('self') would otherwise allow.
		ctype = "application/octet-stream"
	}
	if strings.HasPrefix(ctype, "text/") {
		ctype += "; charset=utf-8"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	if v := mime.FormatMediaType(disp, map[string]string{"filename": name}); v != "" {
		h.Set("Content-Disposition", v)
	} else {
		h.Set("Content-Disposition", disp)
	}
	if ctype == "application/pdf" {
		// Browser PDF viewers refuse to run under a sandbox CSP; allow framing by us only.
		h.Set("Content-Security-Policy", "frame-ancestors 'self'")
		h.Set("X-Frame-Options", "SAMEORIGIN")
	} else {
		h.Set("Content-Security-Policy", fileCSP)
	}
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", time.Unix(info.ModTime, 0), f)
}

func serveThumb(w http.ResponseWriter, r *http.Request, p string) {
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "no thumbnail")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "no thumbnail")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("Content-Security-Policy", fileCSP)
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func zipHeaders(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/zip")
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}); v != "" {
		w.Header().Set("Content-Disposition", v)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", fileCSP)
}
