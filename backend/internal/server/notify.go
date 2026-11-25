package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/oddomens/coffer/internal/store"
)

// notify records an in-app notification. It never fails the caller.
func (s *Server) notify(userID int64, kind, title, body, link string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n := &store.Notification{UserID: userID, Kind: kind, Title: truncate(title, 200), Body: truncate(body, 500), Link: link}
	if err := s.store.Notify(ctx, n); err != nil {
		s.log.Warn("notification failed", "err", err)
	}
}

// notifyMail records a notification and also emails it to users who gave
// an address and did not turn email notifications off.
func (s *Server) notifyMail(userID int64, kind, title, body, link string) {
	s.notify(userID, kind, title, body, link)
	go s.emailUser(userID, title, title, body, link)
}

// prefBool reads a boolean from a user's JSON preferences.
func prefBool(prefs, key string, def bool) bool {
	var m map[string]any
	if json.Unmarshal([]byte(prefs), &m) != nil {
		return def
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func (s *Server) notifyAdmins(kind, title, body, link string) {
	admins, err := s.store.Admins(context.Background())
	if err != nil {
		return
	}
	for _, a := range admins {
		s.notify(a.ID, kind, title, body, link)
	}
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, unread, err := s.store.Notifications(r.Context(), u.ID, 50)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "unread": unread})
}

func (s *Server) handleNotificationsRead(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.MarkNotificationsRead(r.Context(), u.ID, req.IDs); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Per-user interface preferences, stored as a small JSON object.

func (s *Server) handlePrefsGet(w http.ResponseWriter, r *http.Request, u *store.User) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if u.Prefs == "" {
		u.Prefs = "{}"
	}
	_, _ = w.Write([]byte(u.Prefs))
}

func (s *Server) handlePrefsPut(w http.ResponseWriter, r *http.Request, u *store.User) {
	var prefs map[string]any
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := dec.Decode(&prefs); err != nil || prefs == nil {
		badRequest(w, "preferences must be a JSON object under 16 KB")
		return
	}
	b, _ := json.Marshal(prefs)
	u.Prefs = string(b)
	if err := s.store.SetPrefs(r.Context(), u.ID, u.Prefs); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
