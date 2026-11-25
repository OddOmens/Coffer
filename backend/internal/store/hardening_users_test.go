package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coffer.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestDatabaseIsPrivate(t *testing.T) {
	_, path := openTest(t)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("database readable by others: %v %v", info.Mode(), err)
	}
	// An existing world-readable database is tightened on open.
	dir := t.TempDir()
	p := filepath.Join(dir, "old.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	os.Chmod(p, 0o644)
	if s, err = Open(p); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Fatalf("existing database left at %v", info.Mode().Perm())
	}
}

func TestLoginWritesAreGuarded(t *testing.T) {
	s, _ := openTest(t)
	ctx := t.Context()
	u := &User{Username: "bob", PasswordHash: "h1", Role: "user", Scope: "."}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	stale := *u

	// TOTP steps are claimed once.
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 100); !ok {
		t.Fatal("first use rejected")
	}
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 100); ok {
		t.Fatal("step accepted twice")
	}
	if ok, _ := s.UseTOTPStep(ctx, u.ID, 99); ok {
		t.Fatal("older step accepted")
	}

	// An admin resets the password while a sign-in holds the old row.
	if ok, _ := s.SetPassword(ctx, u.ID, "h1", "h2"); !ok {
		t.Fatal("password change failed")
	}
	if ok, _ := s.SetPassword(ctx, u.ID, "h1", "rehash-of-h1"); ok {
		t.Fatal("stale rehash overwrote the reset password")
	}
	sess := &Session{ID: "s1", UserID: u.ID, CreatedAt: 1, LastSeen: 1, ExpiresAt: 1 << 40}
	if err := s.CreateSession(ctx, sess, "t1", &stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session created for a reset account: %v", err)
	}
	cur, _ := s.UserByID(ctx, u.ID)
	if err := s.CreateSession(ctx, sess, "t1", cur); err != nil {
		t.Fatal(err)
	}

	// And a disabled one.
	cur.Disabled = true
	s.UpdateUser(ctx, cur)
	if err := s.CreateSession(ctx, &Session{ID: "s2", UserID: u.ID}, "t2", cur); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session created for a disabled account: %v", err)
	}
}
