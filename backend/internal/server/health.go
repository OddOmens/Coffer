package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

// Health alerts. The box may run for years with nobody looking at it, so a
// check every few minutes tells admins (in the app, and by email when it's
// set up) when something goes wrong and again when it's fixed. Only changes
// alert: a problem that stays is repeated at most once a day.

const (
	healthEvery   = 5 * time.Minute
	healthRemind  = 24 * time.Hour
	scanFailLimit = 3 // full scans in a row
	workerStale   = 30 * time.Minute
	backupStale   = 48 * time.Hour // two nightly runs
	spaceWarn     = 0.10
	spaceCritical = 0.03
)

type healthProblem struct {
	Key      string `json:"key"`      // stable per condition, such as "drive:files"
	Severity string `json:"severity"` // warning | critical
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Link     string `json:"link"`  // the settings page that deals with it
	Since    int64  `json:"since"` // when it was first seen
	Fixed    string `json:"-"`     // the title once it's resolved
}

type openProblem struct {
	healthProblem
	FixedTitle string `json:"fixed"`
	Alerted    int64  `json:"alerted"` // last alert for this condition (may be from an earlier spell)
	Told       bool   `json:"told"`    // an alert went out this spell, so its end is announced too
}

// healthState is kept in the settings table, so a restart neither repeats
// alerts nor forgets what was already said.
type healthState struct {
	Open   map[string]*openProblem `json:"open"`
	Recent map[string]int64        `json:"recent"` // last alert of conditions that cleared, against flapping
	Scans  map[string]scanTrack    `json:"scans"`
	Mail   mailTrack               `json:"mail"`
}

type scanTrack struct {
	Started int64 `json:"started"` // the last finished scan seen
	Fails   int   `json:"fails"`   // in a row
}

type mailTrack struct {
	OK     int64  `json:"ok"`
	Failed int64  `json:"failed"`
	Error  string `json:"error,omitempty"`
}

type healthAlert struct {
	p        healthProblem
	resolved bool
}

type healthTracker struct {
	mu       sync.Mutex
	state    *healthState // loaded on first use
	problems atomic.Pointer[[]healthProblem]
	mail     mailTrack // newest outcomes, merged into the state on each check
	pruned   time.Time // the last daily prune
}

func newHealthState() *healthState {
	return &healthState{Open: map[string]*openProblem{}, Recent: map[string]int64{}, Scans: map[string]scanTrack{}}
}

// update compares the conditions found now with the open ones and returns
// what to tell admins. Problems under a key prefix in unknown (their check
// failed this time) are left as they were.
func (st *healthState) update(now int64, current []healthProblem, unknown []string) []healthAlert {
	remind := int64(healthRemind / time.Second)
	var out []healthAlert
	seen := map[string]bool{}
	for _, p := range current {
		seen[p.Key] = true
		o := st.Open[p.Key]
		escalated := false
		if o == nil {
			o = &openProblem{Alerted: st.Recent[p.Key]}
			o.healthProblem = p
			o.Since = now
			delete(st.Recent, p.Key)
			st.Open[p.Key] = o
		} else {
			escalated = p.Severity == "critical" && o.Severity != "critical"
			since := o.Since
			o.healthProblem = p
			o.Since = since
		}
		o.FixedTitle = p.Fixed
		if escalated || now-o.Alerted >= remind {
			o.Alerted, o.Told = now, true
			out = append(out, healthAlert{p: o.healthProblem})
		}
	}
	for key, o := range st.Open {
		if seen[key] || hasPrefixAny(key, unknown) {
			continue
		}
		delete(st.Open, key)
		if o.Alerted > 0 {
			st.Recent[key] = o.Alerted
		}
		if o.Told {
			p := o.healthProblem
			p.Fixed = o.FixedTitle
			out = append(out, healthAlert{p: p, resolved: true})
		}
	}
	for key, at := range st.Recent {
		if now-at >= remind {
			delete(st.Recent, key)
		}
	}
	return out
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// list is the open problems, critical first, then oldest first.
func (st *healthState) list() []healthProblem {
	out := make([]healthProblem, 0, len(st.Open))
	for _, o := range st.Open {
		out = append(out, o.healthProblem)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Severity == "critical") != (out[j].Severity == "critical") {
			return out[i].Severity == "critical"
		}
		if out[i].Since != out[j].Since {
			return out[i].Since < out[j].Since
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// mailResult records how an email went, for the "email isn't working" alert.
func (s *Server) mailResult(err error) {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if err == nil {
		s.health.mail.OK = time.Now().Unix()
		return
	}
	s.health.mail.Failed, s.health.mail.Error = time.Now().Unix(), truncate(err.Error(), 200)
}

// loadHealth reads the saved state; the caller holds health.mu.
func (s *Server) loadHealth(ctx context.Context) *healthState {
	if s.health.state != nil {
		return s.health.state
	}
	st := newHealthState()
	if err := s.store.GetJSON(ctx, "health", st); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("health state unreadable; starting afresh", "err", err)
		st = newHealthState()
	}
	if st.Open == nil {
		st.Open = map[string]*openProblem{}
	}
	if st.Recent == nil {
		st.Recent = map[string]int64{}
	}
	if st.Scans == nil {
		st.Scans = map[string]scanTrack{}
	}
	s.health.state = st
	list := st.list()
	s.health.problems.Store(&list)
	return st
}

// healthProblems is the open problems as of the last check.
func (s *Server) healthProblems(ctx context.Context) []healthProblem {
	if p := s.health.problems.Load(); p != nil {
		return *p
	}
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	return s.loadHealth(ctx).list()
}

func (s *Server) healthLoop(ctx context.Context) {
	// Give drives, the worker and the first scans a moment after a start.
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	t := time.NewTicker(healthEvery)
	defer t.Stop()
	for {
		s.checkHealth(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) checkHealth(ctx context.Context) {
	s.health.mu.Lock()
	st := s.loadHealth(ctx)
	if s.health.mail.OK > st.Mail.OK {
		st.Mail.OK = s.health.mail.OK
	}
	if s.health.mail.Failed > st.Mail.Failed {
		st.Mail.Failed, st.Mail.Error = s.health.mail.Failed, s.health.mail.Error
	}
	now := time.Now()
	current, unknown := s.probeHealth(ctx, st, now)
	alerts := st.update(now.Unix(), current, unknown)
	if err := s.store.PutJSON(ctx, "health", st); err != nil {
		s.log.Warn("saving health state", "err", err)
	}
	list := st.list()
	s.health.problems.Store(&list)
	s.health.mu.Unlock()
	s.sendHealthAlerts(ctx, alerts)
}

func (s *Server) sendHealthAlerts(ctx context.Context, alerts []healthAlert) {
	if len(alerts) == 0 {
		return
	}
	admins, err := s.store.Admins(ctx)
	if err != nil {
		return
	}
	email := s.cur().HealthEmail
	for _, a := range alerts {
		title, body := a.p.Title, a.p.Detail
		if a.resolved {
			title, body = a.p.Fixed, ""
		}
		s.log.Info("health", "problem", a.p.Key, "resolved", a.resolved, "title", title)
		for _, u := range admins {
			s.notify(u.ID, "health", title, body, a.p.Link)
			// A broken mail server can't report itself by mail.
			if email && a.p.Key != "mail" {
				s.emailUser(u.ID, s.cur().AppName+": "+title, title, body, a.p.Link)
			}
		}
	}
}

// probeHealth looks at everything that can go wrong unnoticed. unknown
// lists key prefixes it couldn't check this time.
func (s *Server) probeHealth(ctx context.Context, st *healthState, now time.Time) (out []healthProblem, unknown []string) {
	drives := s.driveList()
	present := map[string]bool{}
	for _, m := range drives {
		present[m.info.ID] = true
		if m.info.Disabled {
			continue
		}
		name := "“" + m.info.Name + "”"
		if m.d == nil {
			detail := ""
			if m.err != nil {
				detail = truncate(m.err.Error(), 300)
			}
			out = append(out, healthProblem{Key: "drive:" + m.info.ID, Severity: "critical", Title: "Drive " + name + " is offline",
				Detail: detail, Link: "/admin/drives", Fixed: "Drive " + name + " is back online"})
			continue
		}
		// The search index: a scan that fails once may be a passing hiccup.
		info := s.index.ScanInfo(ctx, m.info.ID)
		tr := st.Scans[m.info.ID]
		if !info.Running && info.Started > 0 && info.Finished >= info.Started && info.Started != tr.Started {
			tr.Started = info.Started
			if info.Error != "" {
				tr.Fails++
			} else {
				tr.Fails = 0
			}
			st.Scans[m.info.ID] = tr
		}
		if tr.Fails >= scanFailLimit {
			out = append(out, healthProblem{Key: "scan:" + m.info.ID, Severity: "warning",
				Title:  "Search can't index " + name,
				Detail: fmt.Sprintf("The last %d scans failed: %s", tr.Fails, truncate(info.Error, 300)),
				Link:   "/admin/drives", Fixed: "Search indexes " + name + " again"})
		}
	}
	for id := range st.Scans {
		if !present[id] {
			delete(st.Scans, id)
		}
	}
	out = append(out, s.spaceProblems(drives)...)

	if p, err := s.backupProblems(ctx, now); err != nil {
		unknown = append(unknown, "backup:")
	} else {
		out = append(out, p...)
	}
	if p, err := s.workerProblem(ctx, now); err != nil {
		unknown = append(unknown, "media-worker")
	} else if p != nil {
		out = append(out, *p)
	}
	if s.mailEnabled() && st.Mail.Failed > st.Mail.OK {
		out = append(out, healthProblem{Key: "mail", Severity: "warning", Title: "Email isn't being sent",
			Detail: st.Mail.Error, Link: "/admin/settings", Fixed: "Email is being sent again"})
	}
	return out, unknown
}

// spaceProblems checks free space on every local drive and on the data
// folder, once per filesystem.
func (s *Server) spaceProblems(drives []*mounted) []healthProblem {
	type fsInfo struct {
		key, names, link string
		free, total      int64
	}
	var order []syscall.Fsid
	byFS := map[syscall.Fsid]*fsInfo{}
	add := func(key, name, link, dir string) {
		var sf syscall.Statfs_t
		if dir == "" || syscall.Statfs(dir, &sf) != nil || sf.Blocks == 0 {
			return
		}
		if f := byFS[sf.Fsid]; f != nil {
			f.names += ", " + name
			return
		}
		order = append(order, sf.Fsid)
		byFS[sf.Fsid] = &fsInfo{key: key, names: name, link: link,
			free: int64(sf.Bavail) * int64(sf.Bsize), total: int64(sf.Blocks) * int64(sf.Bsize)}
	}
	for _, m := range drives {
		if m.d != nil && m.info.Kind == "local" {
			add("space:"+m.info.ID, "“"+m.info.Name+"”", "/admin/drives", m.path)
		}
	}
	add("space:data", "Coffer's data folder", "/admin/monitor", s.cfg.DataDir)
	var out []healthProblem
	for _, id := range order {
		f := byFS[id]
		share := float64(f.free) / float64(f.total)
		if share >= spaceWarn {
			continue
		}
		p := healthProblem{Key: f.key, Severity: "warning", Title: "Space is running low on " + f.names, Link: f.link,
			Detail: fmt.Sprintf("%s free of %s (%.0f%%).", humanBytes(f.free), humanBytes(f.total), share*100),
			Fixed:  "Space on " + f.names + " is fine again"}
		if share < spaceCritical {
			p.Severity, p.Title = "critical", f.names+" is almost full"
		}
		out = append(out, p)
	}
	return out
}

// backupProblems flags enabled backups without a good run in two nights.
func (s *Server) backupProblems(ctx context.Context, now time.Time) ([]healthProblem, error) {
	list, err := s.store.Backups(ctx)
	if err != nil {
		return nil, err
	}
	var out []healthProblem
	for _, b := range list {
		if !b.Enabled {
			continue
		}
		_, at, err := s.store.LastGoodSnapshot(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		ref := max(at, b.CreatedAt)
		if now.Sub(time.Unix(ref, 0)) < backupStale {
			continue
		}
		name := "“" + b.Name + "”"
		p := healthProblem{Key: "backup:" + b.ID, Severity: "warning", Link: "/admin/backups", Fixed: "Backup " + name + " is working again"}
		if at == 0 {
			p.Title = "Backup " + name + " hasn't succeeded yet"
		} else {
			p.Title = fmt.Sprintf("Backup %s hasn't succeeded for %d days", name, int(now.Sub(time.Unix(at, 0)).Hours()/24))
		}
		if b.LastRun != nil && b.LastRun.State == "failed" {
			p.Detail = truncate(b.LastRun.Error, 300)
		}
		if now.Sub(time.Unix(ref, 0)) > 7*24*time.Hour {
			p.Severity = "critical"
		}
		out = append(out, p)
	}
	return out, nil
}

// workerProblem flags a media worker that stopped checking in while
// previews wait. A worker that never checked in isn't set up, which the
// monitoring page already shows.
func (s *Server) workerProblem(ctx context.Context, now time.Time) (*healthProblem, error) {
	v, err := s.store.KV(ctx, "media_worker")
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	beat, _ := strconv.ParseInt(v, 10, 64)
	queued, oldest, err := s.store.MediaQueue(ctx)
	if err != nil {
		return nil, err
	}
	if queued == 0 || now.Sub(time.Unix(beat, 0)) < workerStale || now.Sub(time.Unix(oldest, 0)) < workerStale {
		return nil, nil
	}
	what := fmt.Sprintf("%d previews are", queued)
	if queued == 1 {
		what = "1 preview is"
	}
	return &healthProblem{Key: "media-worker", Severity: "warning", Title: "The media worker has stopped",
		Detail: what + " waiting. It last checked in " + roughly(now.Sub(time.Unix(beat, 0))) + " ago; check the coffer-media container.",
		Link:   "/admin/monitor", Fixed: "The media worker is running again"}, nil
}

// roughly says how long d is in the largest whole unit.
func roughly(d time.Duration) string {
	switch {
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, _ *store.User) {
	writeJSON(w, http.StatusOK, map[string]any{"problems": s.healthProblems(r.Context())})
}
