// Package mail sends email through an SMTP server: invitations, sign-in
// codes for share links and notifications.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host     string
	Port     int
	Security string // starttls | tls | none
	Username string
	Password string
	From     string // address
	FromName string
}

func (c Config) Enabled() bool { return c.Host != "" && c.From != "" }

type Message struct {
	To      string // one address
	ToName  string
	ReplyTo string
	Subject string
	Text    string
	HTML    string
}

var ErrNotConfigured = errors.New("email is not set up")

// ParseAddress validates a single plain email address and returns it
// lower-cased.
func ParseAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 || strings.ContainsAny(s, "\r\n<>") {
		return "", fmt.Errorf("%q is not an email address", s)
	}
	return strings.ToLower(a.Address), nil
}

func clean(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// Build renders msg as a multipart/alternative MIME message.
func Build(c Config, msg Message, now time.Time) ([]byte, error) {
	to, err := ParseAddress(msg.To)
	if err != nil {
		return nil, err
	}
	from, err := ParseAddress(c.From)
	if err != nil {
		return nil, fmt.Errorf("sender: %w", err)
	}
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	addr := func(name, a string) string {
		if name = strings.TrimSpace(clean(name)); name == "" {
			return a
		}
		return (&mail.Address{Name: name, Address: a}).String()
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from[strings.LastIndexByte(from, '@')+1:]
	boundary := "coffer-" + hex.EncodeToString(id)

	h("From", addr(c.FromName, from))
	h("To", addr(msg.ToName, to))
	if msg.ReplyTo != "" {
		if rt, err := ParseAddress(msg.ReplyTo); err == nil {
			h("Reply-To", rt)
		}
	}
	h("Subject", mime.QEncoding.Encode("utf-8", clean(msg.Subject)))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Auto-Submitted", "auto-generated")
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	part := func(ctype, body string) error {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, ctype)
		w := quotedprintable.NewWriter(&b)
		if _, err := w.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		b.WriteString("\r\n")
		return nil
	}
	if err := part("text/plain", msg.Text); err != nil {
		return nil, err
	}
	if msg.HTML != "" {
		if err := part("text/html", msg.HTML); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

// Send delivers one message. It refuses to send a password over an
// unencrypted connection.
func Send(ctx context.Context, c Config, msg Message) error {
	if !c.Enabled() {
		return ErrNotConfigured
	}
	data, err := Build(c, msg, time.Now())
	if err != nil {
		return err
	}
	to, _ := ParseAddress(msg.To)
	from, _ := ParseAddress(c.From)

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hostport := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	d := &net.Dialer{}
	if c.Security == "tls" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", hostport)
	} else {
		conn, err = d.DialContext(ctx, "tcp", hostport)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if err := cl.Hello("localhost"); err != nil {
		return err
	}
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS; pick another security option")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if c.Security == "none" {
			return errors.New("refusing to send a password without encryption; use STARTTLS or TLS")
		}
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fmt.Errorf("sign-in to the mail server failed: %w", err)
		}
	}
	if err := cl.Mail(from); err != nil {
		return err
	}
	if err := cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
