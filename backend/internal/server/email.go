package server

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/oddomens/coffer/internal/mail"
	"github.com/oddomens/coffer/internal/store"
)

func (s *Server) mailConfig() mail.Config {
	st := s.cur()
	c := mail.Config{
		Host: st.SMTPHost, Port: st.SMTPPort, Security: st.SMTPSecurity, Username: st.SMTPUser,
		From: st.SMTPFrom, FromName: st.SMTPFromName,
	}
	if c.FromName == "" {
		c.FromName = st.AppName
	}
	if st.SMTPSecret != "" {
		if pw, err := s.keys.Decrypt(st.SMTPSecret); err == nil {
			c.Password = string(pw)
		}
	}
	return c
}

func validEmail(s string) bool {
	_, err := mail.ParseAddress(s)
	return err == nil
}

func (s *Server) mailEnabled() bool { return s.mailConfig().Enabled() }

// sendMail delivers in the background, retrying a few times. what names
// the message in the audit log.
func (s *Server) sendMail(msg mail.Message, what string) {
	go func() {
		var err error
		for i, wait := range []time.Duration{0, 15 * time.Second, 2 * time.Minute} {
			time.Sleep(wait)
			if err = mail.Send(context.Background(), s.mailConfig(), msg); err == nil {
				s.log.Info("email sent", "what", what, "attempt", i+1)
				s.mailResult(nil)
				return
			}
			if errors.Is(err, mail.ErrNotConfigured) {
				break
			}
		}
		if !errors.Is(err, mail.ErrNotConfigured) {
			s.mailResult(err)
		}
		s.log.Warn("email failed", "what", what, "err", err)
		_ = s.store.Audit(context.Background(), store.AuditEntry{Action: "mail.failed", Detail: what + ": " + truncate(err.Error(), 200)})
	}()
}

// Email templates. Every message has a plain-text and an HTML part.

type emailData struct {
	App      string
	Accent   string
	Logo     string // absolute URL, or empty
	Heading  string
	Lines    []string
	Quote    string // the sender's message
	QuoteBy  string
	Button   string
	Link     string
	Code     string
	Footnote string
	Footer   string
}

var emailTmpl = template.Must(template.New("email").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta name="color-scheme" content="light"></head>
<body style="margin:0;padding:0;background:#f5f6f8;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Roboto,Helvetica,Arial,sans-serif;color:#101828">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f5f6f8;padding:32px 12px"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border:1px solid #eaecf0;border-radius:14px">
<tr><td style="padding:28px 28px 0">
{{if .Logo}}<img src="{{.Logo}}" alt="{{.App}}" height="32" style="height:32px;border-radius:8px">{{else}}<div style="font-weight:600;font-size:15px">{{.App}}</div>{{end}}
</td></tr>
<tr><td style="padding:20px 28px 28px">
<h1 style="margin:0 0 12px;font-size:20px;line-height:28px;font-weight:600">{{.Heading}}</h1>
{{range .Lines}}<p style="margin:0 0 12px;font-size:14px;line-height:22px;color:#475467">{{.}}</p>{{end}}
{{if .Quote}}<div style="margin:16px 0;padding:12px 16px;border-left:3px solid {{.Accent}};background:#f9fafb;border-radius:6px;font-size:14px;line-height:22px;color:#344054;white-space:pre-wrap">{{.Quote}}{{if .QuoteBy}}<div style="margin-top:6px;color:#667085;font-size:13px">— {{.QuoteBy}}</div>{{end}}</div>{{end}}
{{if .Code}}<div style="margin:20px 0;font-size:32px;letter-spacing:8px;font-weight:600;font-family:ui-monospace,Menlo,Consolas,monospace">{{.Code}}</div>{{end}}
{{if .Link}}<table role="presentation" cellpadding="0" cellspacing="0" style="margin:20px 0 8px"><tr><td style="background:{{.Accent}};border-radius:8px">
<a href="{{.Link}}" style="display:inline-block;padding:11px 20px;color:#ffffff;text-decoration:none;font-weight:600;font-size:14px">{{.Button}}</a>
</td></tr></table>
<p style="margin:12px 0 0;font-size:12px;line-height:18px;color:#98a2b3;word-break:break-all">Or paste this into your browser: {{.Link}}</p>{{end}}
{{if .Footnote}}<p style="margin:20px 0 0;font-size:12px;line-height:18px;color:#98a2b3">{{.Footnote}}</p>{{end}}
</td></tr></table>
{{if .Footer}}<p style="margin:16px 0 0;font-size:12px;color:#98a2b3">{{.Footer}}</p>{{end}}
</td></tr></table></body></html>`))

func (s *Server) emailBase(r *http.Request) emailData {
	st := s.cur()
	d := emailData{App: st.AppName, Accent: st.AccentColor, Footer: st.ShareFooter}
	if st.LogoType != "" && r != nil {
		d.Logo = s.baseURL(r) + "/api/branding/logo"
	}
	if !hexColor.MatchString(d.Accent) {
		d.Accent = "#3b5bdb"
	}
	return d
}

// render fills msg's bodies from d.
func renderEmail(msg *mail.Message, d emailData) {
	var html bytes.Buffer
	if err := emailTmpl.Execute(&html, d); err == nil {
		msg.HTML = html.String()
	}
	var t strings.Builder
	t.WriteString(d.Heading + "\n\n")
	for _, l := range d.Lines {
		t.WriteString(l + "\n\n")
	}
	if d.Quote != "" {
		for _, l := range strings.Split(d.Quote, "\n") {
			t.WriteString("> " + l + "\n")
		}
		if d.QuoteBy != "" {
			t.WriteString("> — " + d.QuoteBy + "\n")
		}
		t.WriteString("\n")
	}
	if d.Code != "" {
		t.WriteString(d.Code + "\n\n")
	}
	if d.Link != "" {
		t.WriteString(d.Button + ": " + d.Link + "\n\n")
	}
	if d.Footnote != "" {
		t.WriteString(d.Footnote + "\n")
	}
	if d.Footer != "" {
		t.WriteString("\n" + d.Footer + "\n")
	}
	msg.Text = t.String()
}

// Test message.

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		To string `json:"to"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	to, err := mail.ParseAddress(req.To)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	msg := mail.Message{To: to, Subject: s.cur().AppName + " can send email"}
	d := s.emailBase(r)
	d.Heading = "Email is working"
	d.Lines = []string{"This test message was sent from " + s.cur().AppName + " by " + u.Username + ". Invitations, sign-in codes and notifications will arrive like this."}
	renderEmail(&msg, d)
	err = mail.Send(r.Context(), s.mailConfig(), msg)
	s.mailResult(err)
	if err != nil {
		s.audit(r, u, "", "mail.test_failed", "", truncate(err.Error(), 200))
		writeErr(w, http.StatusBadGateway, "mail_failed", "could not send: "+err.Error())
		return
	}
	s.audit(r, u, "", "mail.test", "", to)
	w.WriteHeader(http.StatusNoContent)
}

// emailUser sends a notification to a user who has an address and wants
// notifications by email.
func (s *Server) emailUser(userID int64, subject, heading, body, link string) {
	if !s.mailEnabled() {
		return
	}
	u, err := s.store.UserByID(context.Background(), userID)
	if err != nil || u.Email == "" || u.Disabled || !prefBool(u.Prefs, "emailNotify", true) {
		return
	}
	msg := mail.Message{To: u.Email, ToName: u.Username, Subject: subject}
	d := s.emailBase(nil)
	d.Heading = heading
	if body != "" {
		d.Lines = []string{body}
	}
	if link != "" && s.cfg.PublicURL != "" {
		// Links into the app only work where the app is reachable; skip the
		// button when only the share hostname is known.
		d.Footnote = "Open " + s.cur().AppName + " to see the details."
	}
	renderEmail(&msg, d)
	s.sendMail(msg, "notify:"+u.Username)
}

// handleMyEmail sets the address notifications go to.
func (s *Server) handleMyEmail(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	e := strings.ToLower(strings.TrimSpace(req.Email))
	if e != "" && !validEmail(e) {
		badRequest(w, "that is not an email address")
		return
	}
	if err := s.store.SetEmail(r.Context(), u.ID, e); err != nil {
		s.fail(w, r, err)
		return
	}
	u.Email = e
	s.audit(r, u, "", "user.email", "", "")
	writeJSON(w, http.StatusOK, s.me(r.Context(), u))
}
