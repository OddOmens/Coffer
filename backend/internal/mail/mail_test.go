package mail

import (
	"strings"
	"testing"
	"time"
)

func TestParseAddress(t *testing.T) {
	for _, ok := range []string{"a@b.co", " Sarah@Acme.test "} {
		if _, err := ParseAddress(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "nope", "a@b.co\r\nBcc: x@y.z", "Sarah <s@acme.test>", "a@b.co, c@d.co"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBuildHeaders(t *testing.T) {
	c := Config{From: "files@studio.test", FromName: "Studio\r\nBcc: evil@x.test"}
	b, err := Build(c, Message{To: "client@acme.test", Subject: "Hi\nBcc: evil@x.test", Text: "hello", HTML: "<p>hello</p>"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(b), "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("header injection: %q", line)
		}
	}
	if !strings.Contains(string(b), "text/html") || !strings.Contains(head, "To: client@acme.test") {
		t.Fatalf("unexpected message:\n%s", b)
	}
}
