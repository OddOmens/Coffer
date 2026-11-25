package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// With every administrator demoted or disabled, reset-password --admin is
// the way back in; any session the account had is signed out.
func TestResetUserRestoresAdmin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "coffer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u := &store.User{Username: "boss", PasswordHash: "x", Role: "user", Scope: "projects"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByName(ctx, "boss")
	now := time.Now().Unix()
	sess := &store.Session{ID: "s1", UserID: u.ID, CreatedAt: now, LastSeen: now, ExpiresAt: now + 3600}
	if err := st.CreateSession(ctx, sess, "hash", u); err != nil {
		t.Fatal(err)
	}
	// Disabled after signing in: sessions can't be created for disabled users.
	u.Disabled = true
	if err := st.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	pw, err := resetUser(ctx, st, "boss", false)
	if err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByName(ctx, "boss")
	if ok, _, _ := security.VerifyPassword(pw, u.PasswordHash); u.IsAdmin() || u.Disabled || !ok {
		t.Fatalf("plain reset: %+v", u)
	}
	if n, _ := st.CountSessions(ctx); n != 0 {
		t.Fatalf("%d sessions survived the reset", n)
	}

	if _, err := resetUser(ctx, st, "boss", true); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByName(ctx, "boss")
	if !u.IsAdmin() || u.Disabled || u.Scope != "." || !u.CanWrite || !u.CanDelete || !u.CanShare {
		t.Fatalf("--admin did not restore an administrator: %+v", u)
	}
	if _, err := resetUser(ctx, st, "nobody", true); err == nil {
		t.Fatal("unknown user was accepted")
	}
	if resetPassword([]string{"--admin"}) != 2 || resetPassword([]string{"-x", "boss"}) != 2 {
		t.Fatal("bad arguments were accepted")
	}
}

func TestPrivateDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	os.Mkdir(dir, 0o755)
	privateDataDir(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("data folder is %v", info.Mode().Perm())
	}
}
