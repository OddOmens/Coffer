package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/store"
)

type ctxKey int

const (
	ipKey ctxKey = iota
	untrustedKey
	secureKey
	userKey
	sessionKey
)

func clientIP(r *http.Request) netip.Addr {
	ip, _ := r.Context().Value(ipKey).(netip.Addr)
	return ip
}

func ipString(r *http.Request) string { return clientIP(r).String() }

// isSecure reports whether the client reached us over HTTPS.
func isSecure(r *http.Request) bool {
	v, _ := r.Context().Value(secureKey).(bool)
	return v
}

// realIP resolves the client address, trusting forwarding headers only from
// the proxies trusted on this listener. The share listener trusts
// COFFER_TRUSTED_PROXIES, the app listener COFFER_APP_TRUSTED_PROXIES: a
// Docker subnet trusted for a tunnel also holds the bridge gateway, which
// every connection through a published port can come from.
func (s *Server) realIP(next http.Handler, trusted func(netip.Addr) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		ip := ap.Addr().Unmap()
		if err != nil {
			ip = netip.IPv4Unspecified()
		}
		secure := r.TLS != nil
		untrusted := false
		if !trusted(ip) {
			for _, h := range []string{"CF-Connecting-IP", "X-Forwarded-For", "X-Real-IP", "Forwarded"} {
				untrusted = untrusted || r.Header.Get(h) != ""
			}
		} else {
			// Walk X-Forwarded-For from the right: each proxy appends the
			// peer it saw, so the first hop that is not ours is the client.
			// Some proxies add a header line instead of appending, so join them.
			if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
				hops := strings.Split(xff, ",")
				for i := len(hops) - 1; i >= 0; i-- {
					a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
					if err != nil {
						break
					}
					ip = a.Unmap()
					if !trusted(ip) {
						break
					}
				}
			}
			// CF-Connecting-IP only as a fallback: proxies other than
			// Cloudflare pass it through from the client untouched, so
			// preferring it would let anyone pick their address (and get past
			// COFFER_APP_NETWORKS, link network allowlists and rate limits).
			if trusted(ip) {
				if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
					ip = a.Unmap()
				}
			}
			secure = secure || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
		}
		ctx := context.WithValue(r.Context(), ipKey, ip)
		ctx = context.WithValue(ctx, untrustedKey, untrusted)
		ctx = context.WithValue(ctx, secureKey, secure)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

const appCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; " +
	"frame-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; " +
	"frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", appCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Share tokens live in URLs, so never leak them through Referer.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		if isSecure(r) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter records the response status. It forwards ReadFrom so file
// downloads still go through sendfile.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var tokenInPath = regexp.MustCompile(`^(/(?:api/)?s/)[^/]+`)

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v != nil && v != http.ErrAbortHandler {
				s.log.Error("panic", "err", v, "path", r.URL.Path)
				if sw.status == 0 {
					writeErr(sw, http.StatusInternalServerError, "internal", "internal server error")
				}
			}
			if v == http.ErrAbortHandler {
				// A handler cut a broken response short; let net/http drop
				// the connection after logging it.
				defer panic(v)
			}
			s.mon.Requests.Add(1)
			s.mon.BytesOut.Add(sw.bytes)
			level := slog.LevelDebug
			switch {
			case sw.status >= 500:
				s.mon.Errors.Add(1)
				level = slog.LevelError
			case strings.HasPrefix(r.URL.Path, "/dav"):
				// Desktop clients are chatty; keep them out of the info log.
			case sw.status >= 400 || r.Method != http.MethodGet:
				level = slog.LevelInfo
			}
			if sw.status >= 400 && sw.status < 500 {
				s.mon.ClientErrs.Add(1)
			}
			s.log.Log(r.Context(), level, "http",
				"method", r.Method,
				"path", tokenInPath.ReplaceAllString(r.URL.Path, "${1}…"),
				"status", sw.status, "bytes", sw.bytes,
				"ms", time.Since(start).Milliseconds(), "ip", ipString(r))
		}()
		next.ServeHTTP(sw, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebDAV clients make bursts of requests; they are rate-limited on
		// failed sign-ins instead.
		if !strings.HasPrefix(r.URL.Path, "/dav") && !s.apiLimit.Allow(ipString(r)) {
			w.Header().Set("Retry-After", "30")
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// JSON helpers.

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: msg, Code: code})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return false
	}
	return true
}

// fail maps an error to an HTTP response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, files.ErrExists), errors.Is(err, fs.ErrExist):
		writeErr(w, http.StatusConflict, "exists", "an item with that name already exists")
	case errors.Is(err, files.ErrInvalidPath):
		writeErr(w, http.StatusBadRequest, "invalid_path", "invalid path")
	case errors.Is(err, files.ErrInvalidName):
		writeErr(w, http.StatusBadRequest, "invalid_name", "that name is not allowed")
	case errors.Is(err, files.ErrIntoSelf):
		writeErr(w, http.StatusBadRequest, "into_self", err.Error())
	case errors.Is(err, files.ErrNotDir):
		writeErr(w, http.StatusBadRequest, "not_dir", "not a folder")
	case errors.Is(err, files.ErrIsDir):
		writeErr(w, http.StatusBadRequest, "is_dir", "that is a folder")
	case errors.Is(err, fs.ErrPermission):
		writeErr(w, http.StatusForbidden, "fs_permission", "the server cannot access that item")
	case errors.Is(err, files.ErrOffset):
		writeErr(w, http.StatusConflict, "offset", err.Error())
	case errors.Is(err, files.ErrTooLarge):
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, files.ErrDamaged):
		s.log.Warn("encrypted file failed to authenticate", "path", tokenInPath.ReplaceAllString(r.URL.Path, "${1}…"),
			"query", r.URL.Query().Get("path"))
		writeErr(w, http.StatusInternalServerError, "damaged", err.Error())
	case driveGone(err):
		writeErr(w, http.StatusServiceUnavailable, "drive_offline", "this drive is offline")
	case errors.Is(err, syscall.EROFS):
		writeErr(w, http.StatusServiceUnavailable, "read_only", "this drive is read-only right now")
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		writeErr(w, http.StatusInsufficientStorage, "no_space", "the drive is full")
	case errors.As(err, new(*quotaError)):
		s.quotaFail(w, err)
	case errors.Is(err, context.Canceled):
		// client went away
	default:
		s.log.Error("request failed", "err", err, "path", tokenInPath.ReplaceAllString(r.URL.Path, "${1}…"))
		writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

// driveGone reports an error from a drive that is offline, or went offline
// (its handle closed) while the request ran.
func driveGone(err error) bool {
	return errors.Is(err, errDriveOffline) || errors.Is(err, files.ErrNotMounted) ||
		errors.Is(err, files.ErrWrongDrive) || errors.Is(err, os.ErrClosed)
}

func forbidden(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusForbidden, "forbidden", msg)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusBadRequest, "bad_request", msg)
}
