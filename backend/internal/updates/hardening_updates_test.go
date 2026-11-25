package updates

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeGitHub string

func (f fakeGitHub) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(f)))}, nil
}

func TestCheckOnlyLinksToWebPages(t *testing.T) {
	c := New("OddOmens/Coffer", "1.0.0")
	c.client = &http.Client{Transport: fakeGitHub(`{"tag_name":"v2.0.0","html_url":"javascript:alert(1)"}`)}
	rel, err := c.Check(t.Context())
	if err != nil || rel == nil {
		t.Fatalf("check: %v %v", rel, err)
	}
	if rel.URL != "" {
		t.Fatalf("non-https release link kept: %q", rel.URL)
	}
}

func TestCheckRecordsBadAnswer(t *testing.T) {
	c := New("OddOmens/Coffer", "1.0.0")
	c.client = &http.Client{Transport: fakeGitHub(`<html>`)}
	if _, err := c.Check(t.Context()); err == nil {
		t.Fatal("bad answer accepted")
	}
	if st := c.Status(true); st.Error == "" || st.CheckedAt == 0 {
		t.Fatalf("failure not recorded: %+v", st)
	}
}
