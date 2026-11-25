package server

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

func TestHealthAlertsOnChangesOnly(t *testing.T) {
	st := newHealthState()
	day := int64(24 * 3600)
	t0 := int64(1_800_000_000)
	full := healthProblem{Key: "space:files", Severity: "warning", Title: "Space is running low", Fixed: "Space is fine again"}

	alerts := st.update(t0, []healthProblem{full}, nil)
	if len(alerts) != 1 || alerts[0].resolved || alerts[0].p.Title != full.Title {
		t.Fatalf("first: %+v", alerts)
	}
	// Still there five minutes later: quiet.
	if a := st.update(t0+300, []healthProblem{full}, nil); len(a) != 0 {
		t.Fatalf("repeat: %+v", a)
	}
	// Getting worse alerts at once.
	crit := full
	crit.Severity, crit.Title = "critical", "Almost full"
	if a := st.update(t0+600, []healthProblem{crit}, nil); len(a) != 1 || a[0].p.Severity != "critical" {
		t.Fatalf("escalation: %+v", a)
	}
	if got := st.list(); len(got) != 1 || got[0].Since != t0 {
		t.Fatalf("since: %+v", got)
	}
	// A day after the last alert, a reminder.
	if a := st.update(t0+600+day, []healthProblem{crit}, nil); len(a) != 1 {
		t.Fatalf("reminder: %+v", a)
	}
	// A check that couldn't run leaves it open.
	if a := st.update(t0+700+day, nil, []string{"space:"}); len(a) != 0 || len(st.Open) != 1 {
		t.Fatalf("unknown: %+v", a)
	}
	// Fixed: one resolved alert with the fixed title.
	a := st.update(t0+800+day, nil, nil)
	if len(a) != 1 || !a[0].resolved || a[0].p.Fixed != "Space is fine again" || len(st.Open) != 0 {
		t.Fatalf("resolved: %+v", a)
	}
	// Flapping back within a day stays quiet, and so does its end.
	if a := st.update(t0+900+day, []healthProblem{full}, nil); len(a) != 0 {
		t.Fatalf("flap: %+v", a)
	}
	if a := st.update(t0+1000+day, nil, nil); len(a) != 0 {
		t.Fatalf("flap end: %+v", a)
	}
	// After a quiet day it's news again.
	if a := st.update(t0+1000+3*day, []healthProblem{full}, nil); len(a) != 1 {
		t.Fatalf("later: %+v", a)
	}
}

func TestHealthChecks(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	s := e.srv
	admin, _ := e.store.UserByName(ctx, "admin")

	// Nothing wrong yet.
	s.checkHealth(ctx)
	if p := s.healthProblems(ctx); len(p) != 0 {
		t.Fatalf("healthy box: %+v", p)
	}

	// A drive whose folder is gone, a stopped worker with work waiting,
	// failing mail and a backup that never succeeded.
	if err := e.store.CreateDrive(ctx, &store.Drive{ID: "gone", Name: "Archive", Kind: "local", Config: `{"path":"/nonexistent/archive"}`}); err != nil {
		t.Fatal(err)
	}
	if err := s.loadDrives(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.store.PutKV(ctx, "media_worker", strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.EnqueueMedia(ctx, "files", "projects/acme/renders/final.mp4", store.MediaProxy, 11, 1); err != nil {
		t.Fatal(err)
	}
	st := s.cur()
	st.SMTPHost, st.SMTPFrom = "smtp.invalid", "coffer@example.com"
	s.settings.Store(&st)
	s.mailResult(errors.New("connection refused"))

	s.health.mu.Lock()
	hs := s.loadHealth(ctx)
	got, _ := s.probeHealth(ctx, hs, time.Now().Add(31*time.Minute))
	s.health.mu.Unlock()
	keys := map[string]healthProblem{}
	for _, p := range got {
		keys[p.Key] = p
	}
	if keys["drive:gone"].Severity != "critical" || !strings.Contains(keys["drive:gone"].Title, "Archive") {
		t.Fatalf("offline drive: %+v", got)
	}
	if _, ok := keys["media-worker"]; !ok {
		t.Fatalf("stopped worker: %+v", got)
	}

	b := &store.Backup{ID: "b1", Name: "Nightly", SourceDrive: "files", SourcePath: ".", TargetDrive: "files", TargetPath: ".backups/x", Keep: 3, Enabled: true}
	if err := e.store.CreateBackup(ctx, b); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.backupProblems(ctx, time.Now()); len(p) != 0 {
		t.Fatalf("new backup flagged: %+v", p)
	}
	if p, _ := s.backupProblems(ctx, time.Now().Add(49*time.Hour)); len(p) != 1 || p[0].Key != "backup:b1" {
		t.Fatalf("stale backup: %+v", p)
	}

	// The real check alerts admins once, mail problems in the app only.
	s.checkHealth(ctx)
	list, _, _ := e.store.Notifications(ctx, admin.ID, 50)
	n := 0
	for _, x := range list {
		if x.Kind == "health" {
			n++
		}
	}
	if n < 2 { // the drive and mail (the worker isn't 30 minutes stale by wall clock)
		t.Fatalf("health notifications: %d %+v", n, list)
	}
	s.checkHealth(ctx)
	list2, _, _ := e.store.Notifications(ctx, admin.ID, 50)
	if len(list2) != len(list) {
		t.Fatalf("repeated alerts: %d -> %d", len(list), len(list2))
	}

	// Mail works again: resolved.
	s.mailResult(nil)
	s.checkHealth(ctx)
	list3, _, _ := e.store.Notifications(ctx, admin.ID, 50)
	if len(list3) != len(list2)+1 || list3[0].Title != "Email is being sent again" {
		t.Fatalf("resolved: %+v", list3[0])
	}

	// The state survives a restart: nothing is repeated.
	s.health.state = nil
	s.health.problems.Store(nil)
	if p := s.healthProblems(ctx); len(p) == 0 {
		t.Fatal("state not saved")
	}
	s.checkHealth(ctx)
	list4, _, _ := e.store.Notifications(ctx, admin.ID, 50)
	if len(list4) != len(list3) {
		t.Fatalf("alerts after restart: %d -> %d", len(list3), len(list4))
	}

	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("GET", "/api/admin/health", nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "drive:gone") {
		t.Fatalf("api: %s", body)
	}
}
