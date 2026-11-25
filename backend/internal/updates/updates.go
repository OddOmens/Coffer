// Package updates checks GitHub for a newer Coffer release. It sends no
// identifying information: just the version in the User-Agent.
package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Release struct {
	Version     string `json:"version"`
	URL         string `json:"url"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
}

type Status struct {
	Current   string   `json:"current"`
	Latest    *Release `json:"latest,omitempty"`
	Available bool     `json:"available"`
	CheckedAt int64    `json:"checkedAt"`
	Error     string   `json:"error,omitempty"`
	Enabled   bool     `json:"enabled"`
}

type Checker struct {
	repo    string
	current string
	client  *http.Client

	mu     sync.Mutex
	status Status
}

func New(repo, current string) *Checker {
	return &Checker{repo: repo, current: current, client: &http.Client{Timeout: 15 * time.Second},
		status: Status{Current: current}}
}

func (c *Checker) Status(enabled bool) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	s.Enabled = enabled
	return s
}

// Check fetches the latest release. It returns the release when it is newer
// than the running version.
func (c *Checker) Check(ctx context.Context) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+c.repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Coffer/"+c.current)
	resp, err := c.client.Do(req)
	if err == nil && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err = fmt.Errorf("GitHub answered %s", resp.Status)
	}
	if err != nil {
		c.mu.Lock()
		c.status.CheckedAt, c.status.Error = time.Now().Unix(), err.Error()
		c.mu.Unlock()
		return nil, err
	}
	defer resp.Body.Close()
	var gh struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&gh); err != nil {
		c.mu.Lock()
		c.status.CheckedAt, c.status.Error = time.Now().Unix(), "unreadable answer from GitHub"
		c.mu.Unlock()
		return nil, err
	}
	// The admin page links to it; only ever a web page.
	if !strings.HasPrefix(gh.HTMLURL, "https://") {
		gh.HTMLURL = ""
	}
	rel := &Release{Version: strings.TrimPrefix(gh.TagName, "v"), URL: gh.HTMLURL, Notes: truncate(gh.Body, 4000), PublishedAt: gh.PublishedAt}
	newer := !gh.Draft && !gh.Prerelease && Newer(rel.Version, c.current)
	c.mu.Lock()
	c.status = Status{Current: c.current, Latest: rel, Available: newer, CheckedAt: time.Now().Unix()}
	c.mu.Unlock()
	if newer {
		return rel, nil
	}
	return nil, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Newer reports whether semantic version a is newer than b. "dev" is never
// considered older than a release, so development builds don't nag.
func Newer(a, b string) bool {
	if b == "dev" || b == "" {
		return false
	}
	pa, pb := parse(a), parse(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) [3]int {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
