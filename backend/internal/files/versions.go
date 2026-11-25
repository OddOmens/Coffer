package files

import (
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Versioned names: "spot_v12.mov", "Spot v3 final.aep", "logo-V002.png".
// Everything after the version number up to the extension is ignored, so
// "spot_v12_final.mov" stacks with "spot_v11.mov".
var versionRe = regexp.MustCompile(`(?i)^(.*?)[\s._-]+v(\d{1,4})(?:[\s._-].*)?$`)

// VersionOf returns the stack key and version number of a versioned file
// name, or ok=false for an unversioned name.
func VersionOf(name string) (key string, v int, ok bool) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	m := versionRe.FindStringSubmatch(base)
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return "", 0, false
	}
	v, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return strings.ToLower(strings.TrimSpace(m[1])) + "\x00" + strings.ToLower(ext), v, true
}

// Newer reports whether a is a later version than b.
func Newer(a, b Entry) bool {
	_, va, _ := VersionOf(a.Name)
	_, vb, _ := VersionOf(b.Name)
	if va != vb {
		return va > vb
	}
	return a.ModTime > b.ModTime
}

// LatestVersion returns the newest file in p's folder that stacks with p,
// or p itself.
func LatestVersion(ctx context.Context, d Drive, p string) (Entry, error) {
	cur, err := d.Stat(ctx, p)
	if err != nil {
		return Entry{}, err
	}
	key, _, ok := VersionOf(cur.Name)
	if !ok {
		return cur, nil
	}
	es, err := d.List(ctx, Parent(p), true)
	if err != nil {
		return cur, nil
	}
	best := cur
	for _, e := range es {
		if e.IsDir {
			continue
		}
		if k, _, ok := VersionOf(e.Name); ok && k == key && Newer(e, best) {
			best = e
		}
	}
	return best, nil
}
