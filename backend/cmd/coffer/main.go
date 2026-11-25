// Command coffer runs the Coffer file server.
//
//	coffer                         start the server
//	coffer healthcheck             exit 0 if the local server answers (Docker HEALTHCHECK)
//	coffer reset-password [--admin] <user>
//	                               set a new random password and print it; --admin
//	                               also makes the user an enabled administrator
//	coffer worker                  make video and photo previews (needs ffmpeg and ImageMagick)
//	coffer version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oddomens/coffer/internal/config"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/server"
	"github.com/oddomens/coffer/internal/store"
	"github.com/oddomens/coffer/internal/webui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "reset-password":
			os.Exit(resetPassword(os.Args[2:]))
		case "worker":
			if err := runWorker(); err != nil {
				slog.Error("fatal", "err", err)
				os.Exit(1)
			}
			return
		case "version":
			fmt.Println(version)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogJSON {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	slog.SetDefault(log)
	syscall.Umask(cfg.Umask)

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	privateDataDir(cfg.DataDir, log)
	st, err := store.Open(filepath.Join(cfg.DataDir, "coffer.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	keys, err := security.LoadKeys(cfg.DataDir, cfg.SecretKey)
	if err != nil {
		return err
	}
	idx, err := index.Open(filepath.Join(cfg.DataDir, "index.db"))
	if err != nil {
		return err
	}
	defer idx.Close()
	if err := bootstrapAdmin(st, cfg, log); err != nil {
		return err
	}

	srv, err := server.New(server.Options{
		Config: cfg, Store: st, Index: idx, Keys: keys, Log: log, Version: version, WebUI: webui.FS(),
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go srv.RunJobs(ctx)

	servers := []*http.Server{newHTTPServer(cfg.Listen, srv.AppHandler())}
	if cfg.PublicListen != "" {
		servers = append(servers, newHTTPServer(cfg.PublicListen, srv.PublicHandler()))
	}
	for _, hs := range servers {
		hs.ConnState = srv.ConnState
	}
	errc := make(chan error, len(servers))
	for i, hs := range servers {
		kind := "app"
		if i == 1 {
			kind = "public shares"
		}
		log.Info("listening", "addr", hs.Addr, "serves", kind, "version", version)
		go func() {
			if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, hs := range servers {
		_ = hs.Shutdown(sctx)
	}
	return nil
}

// runWorker runs only the media worker, against the same data directory as
// the server (the coffer-media container).
func runWorker() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	slog.SetDefault(log)
	syscall.Umask(cfg.Umask)
	if !server.MediaToolsInstalled() {
		return errors.New("ffmpeg is not installed; use the coffer-media image")
	}
	// Wait for the server to create the database on a first start.
	db := filepath.Join(cfg.DataDir, "coffer.db")
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(db); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	// The server may still be migrating the database; retry until it's done.
	var st *store.Store
	for i := 0; ; i++ {
		if st, err = store.Open(db); err == nil {
			break
		}
		if i == 30 {
			return err
		}
		time.Sleep(2 * time.Second)
	}
	defer st.Close()
	keys, err := security.LoadKeys(cfg.DataDir, cfg.SecretKey)
	if err != nil {
		return err
	}
	idx, err := index.Open(filepath.Join(cfg.DataDir, "index.db"))
	if err != nil {
		return err
	}
	defer idx.Close()
	srv, err := server.New(server.Options{Config: cfg, Store: st, Index: idx, Keys: keys, Log: log, Version: version, WebUI: webui.FS(), Worker: true})
	if err != nil {
		return err
	}
	defer srv.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv.RunMediaWorker(ctx)
	return nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	// No overall read/write timeouts: multi-GB downloads and slow uploads are
	// legitimate. Header and idle timeouts stop slowloris-style holds.
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

func bootstrapAdmin(st *store.Store, cfg *config.Config, log *slog.Logger) error {
	ctx := context.Background()
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	password := cfg.AdminPassword
	generated := password == ""
	if generated {
		password = security.RandomToken(15)
	} else if len(password) < security.MinPasswordLength {
		return fmt.Errorf("COFFER_ADMIN_PASSWORD must be at least %d characters", security.MinPasswordLength)
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	u := &store.User{Username: cfg.AdminUser, PasswordHash: hash, Role: "admin", Scope: ".",
		CanWrite: true, CanDelete: true, CanShare: true}
	if err := st.CreateUser(ctx, u); err != nil {
		return err
	}
	if generated {
		log.Warn("created first administrator: sign in and change this password now",
			"username", u.Username, "password", password)
	} else {
		log.Info("created first administrator from COFFER_ADMIN_PASSWORD", "username", u.Username)
	}
	return nil
}

func resetPassword(args []string) int {
	makeAdmin := false
	if len(args) == 2 && args[0] == "--admin" {
		makeAdmin, args = true, args[1:]
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: coffer reset-password [--admin] <username>")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "coffer.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer st.Close()
	password, err := resetUser(context.Background(), st, args[0], makeAdmin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("New password for %s: %s\n", args[0], password)
	if makeAdmin {
		fmt.Printf("%s is now an administrator.\n", args[0])
	}
	return 0
}

// resetUser sets a new random password, re-enables the account and signs it
// out everywhere. makeAdmin also restores full administrator rights: the way
// back in when no administrator is left.
func resetUser(ctx context.Context, st *store.Store, username string, makeAdmin bool) (string, error) {
	u, err := st.UserByName(ctx, username)
	if err != nil {
		return "", fmt.Errorf("user %q not found", username)
	}
	password := security.RandomToken(15)
	if u.PasswordHash, err = security.HashPassword(password); err != nil {
		return "", err
	}
	u.Disabled = false
	if makeAdmin {
		u.Role, u.Scope, u.CanWrite, u.CanDelete, u.CanShare = "admin", ".", true, true, true
	}
	if err := st.UpdateUser(ctx, u); err != nil {
		return "", err
	}
	// Whoever had a session with the old password loses it, and so do the
	// app passwords made under it (WebDAV), as with a reset in the app.
	if err := st.DeleteUserSessions(ctx, u.ID, ""); err != nil {
		return "", err
	}
	if _, err := st.DeleteAppTokens(ctx, u.ID); err != nil {
		return "", err
	}
	return password, nil
}

// privateDataDir keeps the database and secret.key away from other local
// users: MkdirAll leaves an existing folder's permissions as they were.
func privateDataDir(dir string, log *slog.Logger) {
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		log.Warn("the data folder is readable by other users; make it private to the Coffer user", "dir", dir, "err", err)
	}
}

func healthcheck() int {
	addr := os.Getenv("COFFER_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/api/health")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
