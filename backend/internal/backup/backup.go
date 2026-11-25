// Package backup makes versioned, deduplicated copies of a folder on one
// drive (usually local) into a folder on another (usually an R2 bucket).
//
// Layout under the target folder:
//
//	objects/<aa>/<sha256 of path>-<mtime>-<size>   one object per file version
//	snapshots/<20261025T020000Z>.json.gz           what the folder looked like
//
// On an encrypted target drive the path hash is keyed (HMAC-SHA256 with a
// key from the drive key), so the bucket can't confirm a guessed path.
//
// A run walks the source and uploads only file versions the target doesn't
// have yet, judged by path, size and modification time. Snapshots older than
// the retention are deleted with any objects no remaining snapshot uses.
package backup

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oddomens/coffer/internal/files"
)

const nameFormat = "20060102T150405Z"

// File is one entry of a snapshot. P is relative to the backed-up folder.
type File struct {
	P string `json:"p"`
	S int64  `json:"s"`
	M int64  `json:"m"`
	K string `json:"k"` // object name
}

type Manifest struct {
	Version int      `json:"v"`
	Created int64    `json:"created"`
	Source  string   `json:"source"`
	Files   []File   `json:"files"`
	Dirs    []string `json:"dirs"` // empty folders, so restores keep them
	// Partial marks a run that missed something: files that couldn't be
	// copied or folders that couldn't be read. Prune doesn't count on it.
	Partial bool `json:"partial,omitempty"`
}

type Snapshot struct {
	Name    string `json:"name"`
	Created int64  `json:"created"`
}

type Progress struct {
	Files         int64 `json:"files"`
	Bytes         int64 `json:"bytes"`
	Uploaded      int64 `json:"uploaded"`
	UploadedBytes int64 `json:"uploadedBytes"`
	// Missing are picked items (drive paths) that weren't there.
	Missing []string `json:"missing,omitempty"`
	// Failed counts files that couldn't be copied even after retries. The
	// snapshot keeps their last backed-up version, if any, and the next run
	// tries them again. Errors holds the first few, as "path: error".
	Failed int64    `json:"failed"`
	Errors []string `json:"errors,omitempty"`
	// Unreadable counts folders (or files) on the source that couldn't be
	// read, so nothing in them was backed up. UnreadablePaths holds the
	// first few.
	Unreadable      int      `json:"unreadable"`
	UnreadablePaths []string `json:"unreadablePaths,omitempty"`
}

// Options change how a run behaves.
type Options struct {
	// Force makes a snapshot even when the source shrank so much that Run
	// would hold it back (see ShrunkError).
	Force bool
}

// ShrunkError is a run held back because the source has far fewer files
// or bytes than the last snapshot: a disk that isn't mounted, a folder
// moved away, or files deleted by mistake. No snapshot is made, so pruning
// can't push out the good ones.
type ShrunkError struct {
	Files, Bytes         int64 // now
	PrevFiles, PrevBytes int64 // in the last snapshot
}

func (e *ShrunkError) Error() string {
	return fmt.Sprintf("the source has %s (%s), down from %s (%s) in the last snapshot",
		count(e.Files, "file"), humanBytes(e.Bytes), count(e.PrevFiles, "file"), humanBytes(e.PrevBytes))
}

func count(n int64, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Below these, a smaller source isn't held back for its file count or size
// alone (an empty source always is, after a snapshot that had files).
const (
	shrinkMinFiles = 20
	shrinkMinBytes = 100 << 20
)

// shrunk reports whether a source of files/bytes is less than half of the
// last snapshot's.
func shrunk(files, bytes, prevFiles, prevBytes int64) bool {
	switch {
	case prevFiles > 0 && files == 0:
		return true
	case prevFiles >= shrinkMinFiles && files*2 < prevFiles:
		return true
	case prevBytes >= shrinkMinBytes && bytes*2 < prevBytes:
		return true
	}
	return false
}

// A file that fails to copy is tried again after each of retryWaits.
var retryWaits = []time.Duration{5 * time.Second, 30 * time.Second}

const (
	maxErrors = 50
	// A run stops after this many files in a row fail: the target is likely
	// down, and every remaining file would just wait out its retries.
	maxFailedInARow = 20
)

func objectName(p string, mtime, size int64, nameKey []byte) string {
	var sum []byte
	if nameKey != nil {
		m := hmac.New(sha256.New, nameKey)
		m.Write([]byte(p))
		sum = m.Sum(nil)
	} else {
		s := sha256.Sum256([]byte(p))
		sum = s[:]
	}
	return fmt.Sprintf("%s-%d-%d", hex.EncodeToString(sum), mtime, size)
}

// nameKeyer is a drive that hides backup object names (an encrypted bucket).
type nameKeyer interface{ NameKey() []byte }

func objectPath(root, name string) string { return join(root, "objects/"+name[:2]+"/"+name) }

func join(root, p string) string {
	if root == "." || root == "" {
		return p
	}
	return root + "/" + p
}

// Items keeps the picked paths that lie inside root (or are root), without
// any that are inside another one, sorted.
func Items(root string, items []string) []string {
	var in []string
	for _, p := range items {
		if files.Within(root, p) {
			in = append(in, p)
		}
	}
	slices.Sort(in)
	in = slices.Compact(in)
	var out []string
	for _, p := range in {
		// Not just the last one: "a b" sorts between "a" and "a/b".
		if !slices.ContainsFunc(out, func(q string) bool { return files.Within(q, p) }) {
			out = append(out, p)
		}
	}
	return out
}

// Run backs up src/srcRoot into dst/dstRoot and returns the snapshot name.
// With only (drive paths inside srcRoot), just those files and folders are
// backed up, still by their path relative to srcRoot. progress is called
// now and then with running totals.
func Run(ctx context.Context, src files.Drive, srcRoot string, only []string, dst files.Drive, dstRoot string, progress func(Progress)) (string, Progress, error) {
	return RunWith(ctx, src, srcRoot, only, dst, dstRoot, progress, Options{})
}

// RunWith is Run with options.
func RunWith(ctx context.Context, src files.Drive, srcRoot string, only []string, dst files.Drive, dstRoot string, progress func(Progress), opt Options) (string, Progress, error) {
	var pr Progress
	unreadable := &files.Unreadable{}
	ctx = files.WithUnreadable(ctx, unreadable)
	var nameKey []byte
	if nk, ok := dst.(nameKeyer); ok {
		nameKey = nk.NameKey()
	}
	known := map[string]bool{}
	prev := map[string]File{} // the last snapshot's files by path
	if snaps, err := Snapshots(ctx, dst, dstRoot); err == nil && len(snaps) > 0 {
		if m, err := Load(ctx, dst, dstRoot, snaps[len(snaps)-1].Name); err == nil {
			for _, f := range m.Files {
				known[f.K] = true
				prev[f.P] = f
			}
		}
	}
	failedInARow := 0
	if err := dst.MkdirAll(ctx, join(dstRoot, "snapshots")); err != nil {
		return "", pr, err
	}
	now := time.Now().UTC()
	m := &Manifest{Version: 1, Created: now.Unix(), Source: srcRoot}
	last := time.Now()
	hasChild := map[string]bool{}
	var dirs []string
	relOf := func(p string) string {
		if srcRoot == "." {
			return p
		}
		return strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
	}
	visit := func(e files.Entry) error {
		rel := relOf(e.Path)
		if rel == "" || rel == "." {
			return nil
		}
		if e.Name == files.InternalDir {
			return fs.SkipDir
		}
		hasChild[path.Dir(rel)] = true
		if e.IsDir {
			dirs = append(dirs, rel)
			return nil
		}
		name := objectName(rel, e.ModTime, e.Size, nameKey)
		m.Files = append(m.Files, File{P: rel, S: e.Size, M: e.ModTime, K: name})
		pr.Files++
		pr.Bytes += e.Size
		if !known[name] && !dst.Exists(ctx, objectPath(dstRoot, name)) {
			if err := copyRetrying(ctx, src, e.Path, dst, objectPath(dstRoot, name), e.Size); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				m.Files = m.Files[:len(m.Files)-1]
				pr.Files--
				pr.Bytes -= e.Size
				if errors.Is(err, fs.ErrNotExist) {
					return nil // deleted while we walked
				}
				// Skip it and carry on, keeping the version backed up last
				// time so this snapshot still restores it.
				pr.Failed++
				if len(pr.Errors) < maxErrors {
					pr.Errors = append(pr.Errors, rel+": "+err.Error())
				}
				if old, ok := prev[rel]; ok {
					m.Files = append(m.Files, old)
					pr.Files++
					pr.Bytes += old.S
				}
				if failedInARow++; failedInARow >= maxFailedInARow {
					return fmt.Errorf("stopped after %d files in a row couldn't be copied, the last %s: %w", failedInARow, rel, err)
				}
				return nil
			}
			failedInARow = 0
			pr.Uploaded++
			pr.UploadedBytes += e.Size
		}
		known[name] = true
		if progress != nil && time.Since(last) > 2*time.Second {
			progress(pr)
			last = time.Now()
		}
		return nil
	}
	var err error
	if only == nil {
		err = src.Walk(ctx, srcRoot, visit)
	} else {
		for _, p := range only {
			if !files.Within(srcRoot, p) {
				pr.Missing = append(pr.Missing, p)
			}
		}
		for _, p := range Items(srcRoot, only) {
			e, serr := src.Stat(ctx, p)
			if errors.Is(serr, fs.ErrNotExist) {
				pr.Missing = append(pr.Missing, p)
				continue
			}
			if serr != nil {
				err = serr
				break
			}
			e.Path = p
			if err = visit(e); errors.Is(err, fs.SkipDir) {
				err = nil
				continue
			}
			if err != nil {
				break
			}
			if !e.IsDir {
				continue
			}
			err = src.Walk(ctx, p, func(e files.Entry) error {
				if e.Path == p {
					return nil // visited above
				}
				return visit(e)
			})
			if err != nil {
				break
			}
		}
	}
	pr.Unreadable, pr.UnreadablePaths = unreadable.Count()
	if err != nil {
		return "", pr, err
	}
	// Compare with the last snapshot, counting only what this run covers
	// (picked items may have been taken out since).
	var prevFiles, prevBytes int64
	for _, f := range prev {
		if only == nil || slices.ContainsFunc(only, func(p string) bool { return files.Within(p, join(srcRoot, f.P)) }) {
			prevFiles++
			prevBytes += f.S
		}
	}
	if !opt.Force && shrunk(pr.Files, pr.Bytes, prevFiles, prevBytes) {
		return "", pr, &ShrunkError{Files: pr.Files, Bytes: pr.Bytes, PrevFiles: prevFiles, PrevBytes: prevBytes}
	}
	m.Partial = pr.Failed > 0 || pr.Unreadable > 0
	for _, d := range dirs {
		if !hasChild[d] {
			m.Dirs = append(m.Dirs, d)
		}
	}
	name := now.Format(nameFormat)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(m); err != nil {
		return "", pr, err
	}
	if err := zw.Close(); err != nil {
		return "", pr, err
	}
	if err := dst.WriteFile(ctx, join(dstRoot, "snapshots/"+name+".json.gz"), &buf, int64(buf.Len())); err != nil {
		return "", pr, err
	}
	if progress != nil {
		progress(pr)
	}
	return name, pr, nil
}

// copyRetrying is copyFile, tried again after a pause when it fails for a
// reason that might pass (a bucket's 500, a dropped connection).
func copyRetrying(ctx context.Context, from files.Drive, src string, to files.Drive, dst string, size int64) error {
	for i := 0; ; i++ {
		err := copyFile(ctx, from, src, to, dst, size)
		if err == nil || i == len(retryWaits) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryWaits[i]):
		}
	}
}

func copyFile(ctx context.Context, from files.Drive, src string, to files.Drive, dst string, size int64) error {
	rc, info, err := from.Open(ctx, src)
	if err != nil {
		return err
	}
	defer rc.Close()
	if info.Size != size {
		size = info.Size
	}
	if err := to.MkdirAll(ctx, files.Parent(dst)); err != nil {
		return err
	}
	return to.WriteFile(ctx, dst, rc, size)
}

// Snapshots lists the snapshots in dst/dstRoot, oldest first.
func Snapshots(ctx context.Context, dst files.Drive, dstRoot string) ([]Snapshot, error) {
	es, err := dst.List(ctx, join(dstRoot, "snapshots"), false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Snapshot{}, nil
		}
		return nil, err
	}
	out := []Snapshot{}
	for _, e := range es {
		name, ok := strings.CutSuffix(e.Name, ".json.gz")
		if !ok || e.IsDir {
			continue
		}
		t, err := time.Parse(nameFormat, name)
		if err != nil {
			continue
		}
		out = append(out, Snapshot{Name: name, Created: t.Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func validName(name string) bool {
	_, err := time.Parse(nameFormat, name)
	return err == nil
}

// Load reads one snapshot's manifest.
func Load(ctx context.Context, dst files.Drive, dstRoot, name string) (*Manifest, error) {
	if !validName(name) {
		return nil, fs.ErrNotExist
	}
	rc, _, err := dst.Open(ctx, join(dstRoot, "snapshots/"+name+".json.gz"))
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	zr, err := gzip.NewReader(rc)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	m := &Manifest{}
	if err := json.NewDecoder(io.LimitReader(zr, 1<<30)).Decode(m); err != nil {
		return nil, err
	}
	if err := m.check(); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", name, err)
	}
	return m, nil
}

var objectRe = regexp.MustCompile(`^[0-9a-f]{64}-(-?[0-9]+)-[0-9]+$`)

// check refuses entries Run could not have written. The manifest is read
// back from the target drive, where someone else may have changed it, and a
// path like "../x" would restore outside the new folder.
func (m *Manifest) check() error {
	for _, f := range m.Files {
		if !validRel(f.P) || !objectRe.MatchString(f.K) {
			return fmt.Errorf("invalid entry %q", f.P)
		}
	}
	for _, d := range m.Dirs {
		if !validRel(d) {
			return fmt.Errorf("invalid folder %q", d)
		}
	}
	return nil
}

func validRel(p string) bool {
	if p == "" || p == "." || path.IsAbs(p) || path.Clean(p) != p || !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return false
	}
	parts := strings.Split(p, "/")
	return !slices.Contains(parts, "..") && !slices.Contains(parts, files.InternalDir)
}

// Retention beyond the newest keep snapshots: the newest of each week for
// keepWeeks weeks and of each month for keepMonths months, so a problem
// noticed late can still be undone.
const (
	keepWeeks  = 8
	keepMonths = 12
)

// now is time.Now, for tests.
var now = time.Now

// retained picks the snapshots to keep: the newest keep, plus the newest of
// each recent week and month. snaps is oldest first.
func retained(snaps []Snapshot, keep int, t time.Time) map[string]bool {
	out := map[string]bool{}
	for _, sn := range snaps[max(0, len(snaps)-keep):] {
		out[sn.Name] = true
	}
	weekFrom := t.AddDate(0, 0, -7*keepWeeks)
	monthFrom := t.AddDate(0, -keepMonths, 0)
	weeks, months := map[[2]int]bool{}, map[[2]int]bool{}
	for i := len(snaps) - 1; i >= 0; i-- {
		c := time.Unix(snaps[i].Created, 0).In(t.Location())
		if c.After(weekFrom) {
			y, w := c.ISOWeek()
			if !weeks[[2]int{y, w}] {
				weeks[[2]int{y, w}] = true
				out[snaps[i].Name] = true
			}
		}
		if c.After(monthFrom) {
			if k := [2]int{c.Year(), int(c.Month())}; !months[k] {
				months[k] = true
				out[snaps[i].Name] = true
			}
		}
	}
	return out
}

// Prune keeps the newest keep snapshots, the newest of each of the last
// weeks and months, and always the newest complete snapshot that has
// files; it removes the other snapshots and the objects none of the kept
// ones use. It returns the number of snapshots and objects removed.
func Prune(ctx context.Context, dst files.Drive, dstRoot string, keep int) (int, int, error) {
	snaps, err := Snapshots(ctx, dst, dstRoot)
	if err != nil || keep <= 0 || len(snaps) <= keep {
		return 0, 0, err
	}
	keepSet := retained(snaps, keep, now())
	// The newest good snapshot stays whatever its age: when every recent
	// run missed things or found nothing, it's the one to restore from.
	// Manifests can be big, so only the last one read is kept in memory.
	var good *Manifest
	goodName := ""
	for i := len(snaps) - 1; i >= 0; i-- {
		m, err := Load(ctx, dst, dstRoot, snaps[i].Name)
		if err != nil {
			// Never delete anything based on a manifest we couldn't read.
			return 0, 0, fmt.Errorf("reading snapshot %s: %w", snaps[i].Name, err)
		}
		if !m.Partial && len(m.Files) > 0 {
			keepSet[snaps[i].Name] = true
			good, goodName = m, snaps[i].Name
			break
		}
	}
	used := map[string]bool{}
	for _, sn := range snaps {
		if !keepSet[sn.Name] {
			continue
		}
		m := good
		if sn.Name != goodName {
			var err error
			if m, err = Load(ctx, dst, dstRoot, sn.Name); err != nil {
				return 0, 0, fmt.Errorf("reading snapshot %s: %w", sn.Name, err)
			}
		}
		for _, f := range m.Files {
			used[f.K] = true
		}
	}
	removedSnaps := 0
	for _, sn := range snaps {
		if keepSet[sn.Name] {
			continue
		}
		if err := dst.RemoveAll(ctx, join(dstRoot, "snapshots/"+sn.Name+".json.gz")); err != nil {
			return removedSnaps, 0, err
		}
		removedSnaps++
	}
	if removedSnaps == 0 {
		return 0, 0, nil
	}
	removedObjs := 0
	root := join(dstRoot, "objects")
	err = dst.Walk(ctx, root, func(e files.Entry) error {
		if e.IsDir || used[e.Name] {
			return nil
		}
		if err := dst.RemoveAll(ctx, e.Path); err != nil {
			return err
		}
		removedObjs++
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return removedSnaps, removedObjs, err
}

// Entry is a file or folder inside a snapshot, for browsing.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"` // "/a/b"
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Items   int64  `json:"items,omitempty"`
}

// Browse lists the direct children of dir ("." for the top) in a snapshot.
func Browse(m *Manifest, dir string) []Entry {
	prefix := ""
	if dir != "." && dir != "" {
		prefix = dir + "/"
	}
	byName := map[string]*Entry{}
	add := func(rel string, f *File) {
		rest, ok := strings.CutPrefix(rel, prefix)
		if !ok || rest == "" {
			return
		}
		name, sub, nested := strings.Cut(rest, "/")
		e := byName[name]
		if e == nil {
			e = &Entry{Name: name, Path: "/" + prefix + name, IsDir: nested || f == nil}
			byName[name] = e
		}
		_ = sub
		if f != nil {
			if nested {
				e.Size += f.S
				e.Items++
				e.ModTime = max(e.ModTime, f.M)
			} else {
				e.Size, e.ModTime = f.S, f.M
			}
		}
	}
	for i := range m.Files {
		add(m.Files[i].P, &m.Files[i])
	}
	for _, d := range m.Dirs {
		add(d, nil)
	}
	out := make([]Entry, 0, len(byName))
	for _, e := range byName {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Restore copies the selected paths (files or folders, relative to the
// backed-up folder; "." for everything) from a snapshot into to/toRoot,
// which should be a new, empty folder.
func Restore(ctx context.Context, dst files.Drive, dstRoot string, m *Manifest, paths []string, to files.Drive, toRoot string) (int, error) {
	match := func(p string) bool {
		for _, sel := range paths {
			if sel == "." || p == sel || strings.HasPrefix(p, sel+"/") {
				return true
			}
		}
		return false
	}
	if err := to.MkdirAll(ctx, toRoot); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range m.Dirs {
		if match(d) {
			if err := to.MkdirAll(ctx, join(toRoot, d)); err != nil {
				return n, err
			}
		}
	}
	for _, f := range m.Files {
		if !match(f.P) {
			continue
		}
		if err := copyFile(ctx, dst, objectPath(dstRoot, f.K), to, join(toRoot, f.P), f.S); err != nil {
			return n, fmt.Errorf("%s: %w", f.P, err)
		}
		n++
	}
	return n, nil
}

// Find returns the file at p (relative to the backed-up folder), or nil.
func (m *Manifest) Find(p string) *File {
	for i := range m.Files {
		if m.Files[i].P == p {
			return &m.Files[i]
		}
	}
	return nil
}

// Open reads one file of a snapshot from the backup drive.
func Open(ctx context.Context, dst files.Drive, dstRoot string, f *File) (io.ReadSeekCloser, error) {
	rc, _, err := dst.Open(ctx, objectPath(dstRoot, f.K))
	return rc, err
}

// WriteZip streams the named entries of dir in a snapshot ("." for the top;
// no names for all of dir) into a zip, with paths relative to dir.
func WriteZip(ctx context.Context, dst files.Drive, dstRoot string, m *Manifest, dir string, names []string, w io.Writer) error {
	prefix := ""
	if dir != "." && dir != "" {
		prefix = dir + "/"
	}
	inZip := func(p string) (string, bool) {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok || rest == "" {
			return "", false
		}
		if first, _, _ := strings.Cut(rest, "/"); len(names) > 0 && !slices.Contains(names, first) {
			return "", false
		}
		// A name made outside Coffer may hold a backslash, which Windows
		// unzips as a separator (and "..\" as a way out).
		return strings.ReplaceAll(rest, `\`, "_"), true
	}
	zw := zip.NewWriter(w)
	for _, d := range m.Dirs {
		if rel, ok := inZip(d); ok {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: rel + "/", Modified: time.Unix(m.Created, 0), Method: zip.Store}); err != nil {
				return err
			}
		}
	}
	for i := range m.Files {
		f := &m.Files[i]
		rel, ok := inZip(f.P)
		if !ok {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr := &zip.FileHeader{Name: rel, Modified: time.Unix(f.M, 0), Method: zip.Store}
		hdr.SetMode(0o644)
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, err := Open(ctx, dst, dstRoot, f)
		if err != nil {
			return fmt.Errorf("%s: %w", f.P, err)
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.P, err)
		}
	}
	return zw.Close()
}
