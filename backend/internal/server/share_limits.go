package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

// Limits on what a link's visitors can make Coffer do on the owner's
// behalf: notifications and emails, verification codes and open uploads.
// They are kept in memory; a restart only starts the counts again.

var (
	// Downloads and uploads through one link (by one verified visitor, or
	// by anyone when the link doesn't ask for an email) notify the owner
	// at most once per window. Later ones are added up into one more
	// notification when the window ends.
	shareEventWindow = 10 * time.Minute
	shareOpenWindow  = time.Hour // "opened" is sent once and not added up

	shareMailPerHour  = 30 // emails caused by one link's visitors, of every kind
	shareCodesPerHour = 20 // verification codes sent for one link
	codesPerAddrHour  = 3  // verification codes asked for one address on one link

	maxShareUploads = 10 // open upload sessions per link, and per visitor IP
)

// counter counts events per key in fixed windows.
type counter struct {
	mu     sync.Mutex
	window time.Duration
	m      map[string]*count
}

type count struct {
	start time.Time
	n     int
}

func newCounter(window time.Duration) *counter {
	return &counter{window: window, m: map[string]*count{}}
}

func (c *counter) get(key string, now time.Time) *count {
	if len(c.m) > 10000 {
		for k, e := range c.m {
			if now.Sub(e.start) >= c.window {
				delete(c.m, k)
			}
		}
	}
	e := c.m[key]
	if e == nil || now.Sub(e.start) >= c.window {
		e = &count{start: now}
		c.m[key] = e
	}
	return e
}

// allow counts one event for key and reports whether it was within limit.
func (c *counter) allow(key string, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.get(key, time.Now())
	if e.n >= limit {
		return false
	}
	e.n++
	return true
}

// full reports whether key has used up its limit, without counting.
func (c *counter) full(key string, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.get(key, time.Now()).n >= limit
}

var (
	shareMail  = newCounter(time.Hour) // by share ID
	shareCodes = newCounter(time.Hour) // by share ID, and share ID + address
)

// shareMailAllowed counts one email caused by a visitor of the link.
func shareMailAllowed(shareID string) bool { return shareMail.allow(shareID, shareMailPerHour) }

// notifyShareOwner records a notification for the owner of a link and emails
// it while the link is under its hourly email cap.
func (s *Server) notifyShareOwner(shareID string, ownerID int64, kind, title, body string) {
	s.notify(ownerID, kind, title, body, "/shares")
	if shareMailAllowed(shareID) {
		go s.emailUser(ownerID, title, title, body, "/shares")
	}
}

// Collapsed owner notifications.

type shareNotice struct {
	start   time.Time
	pending int    // events since start that weren't sent
	last    string // the latest of them
	timer   *time.Timer
}

var (
	noticeMu sync.Mutex
	notices  = map[string]*shareNotice{}
)

func (s *Server) shareEvent(sc *shareCtx, flag int, title, body string) {
	if sc.sh.Notify&flag == 0 {
		return
	}
	key := fmt.Sprintf("%s:%d", sc.sh.ID, flag)
	if sc.viewer != nil {
		key += ":" + sc.viewer.ID
	}
	window := shareEventWindow
	if flag == store.NotifyOpen {
		window = shareOpenWindow
	}
	now := time.Now()
	noticeMu.Lock()
	if n := notices[key]; n != nil && now.Sub(n.start) < window {
		if flag != store.NotifyOpen {
			n.pending++
			n.last = title
			if n.timer == nil {
				shareID, ownerID, name := sc.sh.ID, sc.owner.ID, s.shareName(sc)
				n.timer = time.AfterFunc(n.start.Add(window).Sub(now), func() {
					s.flushShareEvent(key, shareID, ownerID, flag, name)
				})
			}
		}
		noticeMu.Unlock()
		return
	}
	if len(notices) > 10000 {
		for k, n := range notices {
			if n.timer == nil && now.Sub(n.start) >= shareOpenWindow {
				delete(notices, k)
			}
		}
	}
	notices[key] = &shareNotice{start: now}
	noticeMu.Unlock()
	s.notifyShareOwner(sc.sh.ID, sc.owner.ID, "share", title, body)
}

// flushShareEvent sends one notification for the events collapsed during a
// window. It starts a new window, so the next event waits for it too.
func (s *Server) flushShareEvent(key, shareID string, ownerID int64, flag int, name string) {
	noticeMu.Lock()
	n := notices[key]
	if n == nil || n.pending == 0 {
		delete(notices, key)
		noticeMu.Unlock()
		return
	}
	pending, last := n.pending, n.last
	notices[key] = &shareNotice{start: time.Now()}
	noticeMu.Unlock()
	what := "download"
	via := "via"
	if flag == store.NotifyUpload {
		what, via = "upload", "to"
	}
	if pending > 1 {
		what += "s"
	}
	title := fmt.Sprintf("%d more %s %s your link “%s”", pending, what, via, name)
	s.notifyShareOwner(shareID, ownerID, "share", title, "The latest: "+last)
}

// Verification codes.

// codeAllowed reports whether a code may be sent for email on the link. It
// is asked before Coffer knows whether the address is invited, so the
// answer can't be used to probe the invitation list.
func codeAllowed(shareID, email string) bool {
	return !shareCodes.full(shareID, shareCodesPerHour) && !shareMail.full(shareID, shareMailPerHour) &&
		shareCodes.allow(shareID+"\x00"+email, codesPerAddrHour)
}

// codeSent counts a code that is about to be emailed.
func codeSent(shareID string) {
	shareCodes.allow(shareID, shareCodesPerHour)
	shareMailAllowed(shareID)
}

// Open uploads through links.

var (
	shareUploadsMu sync.Mutex
	shareUploadsIP = map[string]map[string]time.Time{} // IP -> upload ID -> started
)

// uploadSlot reports whether the link and the visitor's IP may start
// another upload, and writes the refusal when not.
func (s *Server) uploadSlot(w http.ResponseWriter, r *http.Request, sc *shareCtx) bool {
	ctx := r.Context()
	n, err := s.store.CountShareUploads(ctx, sc.sh.ID)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if n < maxShareUploads && s.ipUploads(ctx, ipString(r)) < maxShareUploads {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many uploads at once, wait for some to finish")
	return false
}

// ipUploads counts the uploads ip started that are still open.
func (s *Server) ipUploads(ctx context.Context, ip string) int {
	shareUploadsMu.Lock()
	ids := make([]string, 0, len(shareUploadsIP[ip]))
	for id, t := range shareUploadsIP[ip] {
		if time.Since(t) < 48*time.Hour {
			ids = append(ids, id)
		}
	}
	shareUploadsMu.Unlock()
	if len(ids) < maxShareUploads {
		return len(ids)
	}
	// Finished, aborted and expired uploads leave the database; forget them.
	open := 0
	for _, id := range ids {
		if _, err := s.store.UploadByID(ctx, id); err == nil {
			open++
		} else {
			forgetUpload(ip, id)
		}
	}
	return open
}

func rememberUpload(ip, id string) {
	shareUploadsMu.Lock()
	defer shareUploadsMu.Unlock()
	if len(shareUploadsIP) > 10000 {
		for k, m := range shareUploadsIP {
			for id, t := range m {
				if time.Since(t) >= 48*time.Hour {
					delete(m, id)
				}
			}
			if len(m) == 0 {
				delete(shareUploadsIP, k)
			}
		}
	}
	m := shareUploadsIP[ip]
	if m == nil {
		m = map[string]time.Time{}
		shareUploadsIP[ip] = m
	}
	m[id] = time.Now()
}

func forgetUpload(ip, id string) {
	shareUploadsMu.Lock()
	defer shareUploadsMu.Unlock()
	delete(shareUploadsIP[ip], id)
	if len(shareUploadsIP[ip]) == 0 {
		delete(shareUploadsIP, ip)
	}
}
