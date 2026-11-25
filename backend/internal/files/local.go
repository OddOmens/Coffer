package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Local is a drive on the server's filesystem. Every operation goes through
// an os.Root, so no path, symlink or race can reach outside the directory.
type Local struct {
	root *os.Root
	dir  string
	// mu makes "check the target is free, then rename onto it" atomic for
	// Coffer's own changes, so two moves can't overwrite each other.
	mu sync.Mutex
}

const (
	uploadsDir = InternalDir + "/uploads"
	trashDir   = InternalDir + "/trash"
)

// OpenLocal opens dir as a drive without checking its marker (see
// OpenLocalDrive).
func OpenLocal(dir string) (*Local, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	return openLocalRoot(root, dir)
}

func openLocalRoot(root *os.Root, dir string) (*Local, error) {
	for _, d := range []string{uploadsDir, trashDir} {
		if err := root.MkdirAll(d, 0o770); err != nil {
			root.Close()
			return nil, fmt.Errorf("create %s in %s: %w", d, dir, err)
		}
	}
	return &Local{root: root, dir: dir}, nil
}

func (l *Local) Kind() string { return "local" }
func (l *Local) Close() error { return l.root.Close() }

func (l *Local) Stat(_ context.Context, p string) (Entry, error) {
	info, err := l.root.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return newEntryFromInfo(path.Base(p), p, info), nil
}

// RealPath resolves the symlinks in p one component at a time. Components
// that don't exist yet (something about to be created) are kept as given.
func (l *Local) RealPath(p string) (string, error) {
	if p == "." {
		return ".", nil
	}
	cur, rest := ".", strings.Split(p, "/")
	for hops := 0; len(rest) > 0; {
		next := join(cur, rest[0])
		info, err := l.root.Lstat(next)
		if err != nil {
			return join(cur, strings.Join(rest, "/")), nil
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			cur, rest = next, rest[1:]
			continue
		}
		if hops++; hops > 40 {
			return "", ErrInvalidPath
		}
		target, err := l.root.Readlink(next)
		if err != nil {
			return "", err
		}
		t := path.Join(cur, target)
		if path.IsAbs(target) || t == ".." || strings.HasPrefix(t, "../") {
			return "", ErrInvalidPath // os.Root would refuse to follow it anyway
		}
		cur, rest = ".", append(strings.Split(t, "/"), rest[1:]...)
	}
	return cur, nil
}

// List resolves symlinks inside the root; links that leave it are skipped.
func (l *Local) List(_ context.Context, p string, hideDot bool) ([]Entry, error) {
	d, err := l.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	des, err := d.ReadDir(-1)
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if Hidden(name, hideDot) {
			continue
		}
		cp := join(p, name)
		info, err := de.Info()
		if err != nil {
			continue
		}
		link := info.Mode()&fs.ModeSymlink != 0
		if link {
			if info, err = l.root.Stat(cp); err != nil {
				continue
			}
		} else if !info.Mode().IsRegular() && !info.IsDir() {
			continue
		}
		e := newEntryFromInfo(name, cp, info)
		e.Symlink = link
		out = append(out, e)
	}
	SortEntries(out)
	return out, nil
}

func SortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

func (l *Local) Open(_ context.Context, p string) (io.ReadSeekCloser, Entry, error) {
	f, err := l.root.Open(p)
	if err != nil {
		return nil, Entry{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Entry{}, err
	}
	if info.IsDir() {
		f.Close()
		return nil, Entry{}, ErrIsDir
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, Entry{}, fs.ErrNotExist
	}
	return f, newEntryFromInfo(path.Base(p), p, info), nil
}

func (l *Local) Exists(_ context.Context, p string) bool {
	_, err := l.root.Lstat(p)
	return err == nil
}

func (l *Local) Mkdir(ctx context.Context, p string) error {
	if l.Exists(ctx, p) {
		return ErrExists
	}
	return l.root.Mkdir(p, 0o775)
}

func (l *Local) MkdirAll(_ context.Context, p string) error {
	if p == "." {
		return nil
	}
	return l.root.MkdirAll(p, 0o775)
}

func (l *Local) Rename(_ context.Context, src, dst string) error {
	if src == "." {
		return ErrInvalidPath
	}
	if dst == src {
		return nil
	}
	if strings.HasPrefix(dst, src+"/") {
		return ErrIntoSelf
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	si, err := l.root.Lstat(src)
	if err != nil {
		return err
	}
	// dst may only exist when it is src under another case (case-insensitive volumes).
	if di, err := l.root.Lstat(dst); err == nil && !os.SameFile(si, di) {
		return ErrExists
	}
	return l.root.Rename(src, dst)
}

func (l *Local) Copy(ctx context.Context, src, dst string) error {
	if strings.HasPrefix(dst, src+"/") || dst == src {
		return ErrIntoSelf
	}
	if l.Exists(ctx, dst) {
		return ErrExists
	}
	info, err := l.root.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return l.copyFile(src, dst, info)
	}
	return fs.WalkDir(l.root.FS(), src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target := dst + strings.TrimPrefix(p, src)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return l.root.Mkdir(target, 0o775)
		case info.Mode().IsRegular():
			return l.copyFile(p, target, info)
		default:
			return nil // symlinks and special files are skipped
		}
	})
}

func (l *Local) copyFile(src, dst string, info fs.FileInfo) error {
	in, err := l.root.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := l.root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return err
	}
	// *os.File to *os.File uses copy_file_range, so this stays in the kernel.
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		l.root.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return l.root.Chtimes(dst, time.Now(), info.ModTime())
}

func (l *Local) RemoveAll(_ context.Context, p string) error {
	if p == "." {
		return ErrInvalidPath
	}
	if _, err := l.root.Lstat(p); err != nil {
		return err
	}
	return l.root.RemoveAll(p)
}

func (l *Local) WriteFile(_ context.Context, p string, r io.Reader, size int64) error {
	tmp := join(Parent(p), writeTmpPrefix+tmpSuffix())
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, r)
	if err == nil && size >= 0 && n != size {
		err = fmt.Errorf("short write: %d of %d bytes", n, size)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		l.root.Remove(tmp)
		return err
	}
	return l.root.Rename(tmp, p)
}

func tmpSuffix() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

const writeTmpPrefix = ".coffer-write-"

// CleanStaleWrites removes WriteFile's temporary files older than age, left
// behind when Coffer stopped halfway through a write. It returns how many
// it removed.
func (l *Local) CleanStaleWrites(ctx context.Context, age time.Duration) (int, error) {
	n := 0
	err := fs.WalkDir(l.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p == InternalDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), writeTmpPrefix) || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil && time.Since(info.ModTime()) > age {
			if l.root.Remove(p) == nil {
				n++
			}
		}
		return nil
	})
	return n, err
}

// Unreadable collects what walks skipped because it couldn't be read (a
// folder without permission, a failing disk), so a backup can say so
// instead of quietly leaving it out.
type Unreadable struct {
	mu    sync.Mutex
	n     int
	paths []string
}

type unreadableKey struct{}

// WithUnreadable makes walks under ctx report what they skip to u.
func WithUnreadable(ctx context.Context, u *Unreadable) context.Context {
	return context.WithValue(ctx, unreadableKey{}, u)
}

func noteUnreadable(ctx context.Context, p string, err error) {
	u, _ := ctx.Value(unreadableKey{}).(*Unreadable)
	if u == nil || errors.Is(err, fs.ErrNotExist) {
		return // gone while we walked: not a read failure
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.n++
	if len(u.paths) < 5 {
		u.paths = append(u.paths, p)
	}
}

// Count returns how many entries were skipped, and the first few paths.
func (u *Unreadable) Count() (int, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.n, slices.Clone(u.paths)
}

func (l *Local) Walk(ctx context.Context, root string, fn func(Entry) error) error {
	return fs.WalkDir(l.root.FS(), root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			// Skipped, and reported to whoever asked (WithUnreadable).
			noteUnreadable(ctx, p, err)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p != root && d.Name() == InternalDir && Parent(p) == "." {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			noteUnreadable(ctx, p, err)
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		return fn(newEntryFromInfo(d.Name(), p, info))
	})
}

func (l *Local) Usage(context.Context) (free, total int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(l.dir, &st); err != nil {
		return 0, 0, false
	}
	return int64(st.Bavail) * st.Bsize, int64(st.Blocks) * st.Bsize, true
}

func (l *Local) Trash(_ context.Context, p, id string) (bool, int64, error) {
	if p == "." {
		return false, 0, ErrInvalidPath
	}
	info, err := l.root.Lstat(p)
	if err != nil {
		return false, 0, err
	}
	if err := l.root.Rename(p, trashDir+"/"+id); err != nil {
		return false, 0, err
	}
	if info.IsDir() {
		return true, 0, nil
	}
	return false, info.Size(), nil
}

func (l *Local) Restore(ctx context.Context, id, orig string) (string, error) {
	dir := Parent(orig)
	if err := l.root.MkdirAll(dir, 0o775); err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	dst := join(dir, UniqueName(ctx, l, dir, path.Base(orig)))
	return dst, l.root.Rename(trashDir+"/"+id, dst)
}

func (l *Local) PurgeTrash(_ context.Context, id string) error {
	err := l.root.RemoveAll(trashDir + "/" + id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// UniqueName returns name, or "name (2).ext" etc. if it is taken in dir.
func UniqueName(ctx context.Context, d Drive, dir, name string) string {
	if !d.Exists(ctx, join(dir, name)) {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !d.Exists(ctx, join(dir, n)) {
			return n
		}
	}
}

// Uploads: chunks are appended to a staging file inside the drive so the
// final move is an atomic rename.

var uploadIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func partPath(id string) (string, error) {
	if !uploadIDRe.MatchString(id) {
		return "", ErrInvalidPath
	}
	return uploadsDir + "/" + id + ".part", nil
}

func (l *Local) ChunkSize() int64 { return 0 }

func (l *Local) StartUpload(_ context.Context, up *UploadState) error {
	pp, err := partPath(up.ID)
	if err != nil {
		return err
	}
	f, err := l.root.OpenFile(pp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o660)
	if err != nil {
		return err
	}
	return f.Close()
}

func (l *Local) UploadOffset(_ context.Context, up *UploadState) (int64, error) {
	pp, err := partPath(up.ID)
	if err != nil {
		return 0, err
	}
	info, err := l.root.Stat(pp)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

var ErrOffset = errors.New("upload offset mismatch")
var ErrTooLarge = errors.New("upload exceeds declared size")

func (l *Local) WriteChunk(_ context.Context, up *UploadState, offset, _ int64, r io.Reader) (int64, error) {
	pp, err := partPath(up.ID)
	if err != nil {
		return 0, err
	}
	f, err := l.root.OpenFile(pp, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() != offset {
		return info.Size(), ErrOffset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	n, err := io.Copy(f, io.LimitReader(r, up.Size-offset+1))
	if offset+n > up.Size {
		f.Truncate(offset)
		return offset, ErrTooLarge
	}
	// On a broken connection whatever arrived is kept; the client resumes.
	return offset + n, err
}

func (l *Local) FinishUpload(ctx context.Context, up *UploadState, trashID string) (string, bool, error) {
	pp, err := partPath(up.ID)
	if err != nil {
		return "", false, err
	}
	info, err := l.root.Stat(pp)
	if err != nil {
		return "", false, err
	}
	if info.Size() != up.Size {
		return "", false, fmt.Errorf("%w: have %d of %d bytes", ErrOffset, info.Size(), up.Size)
	}
	target := join(up.Dir, up.Name)
	parent := Parent(target)
	if err := l.root.MkdirAll(parent, 0o775); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.root.Chmod(pp, 0o664); err != nil {
		return "", false, err
	}
	existing, err := l.root.Lstat(target)
	if err != nil {
		return target, false, l.root.Rename(pp, target)
	}
	if trashID == "" || existing.IsDir() {
		target = join(parent, UniqueName(ctx, l, parent, path.Base(target)))
		return target, false, l.root.Rename(pp, target)
	}
	// Replacing: the old file must never be lost, nor left in the trash
	// with the new one missing. A hard link keeps it in place until the
	// rename swaps the new one in, in one step.
	trashed := trashDir + "/" + trashID
	if err := l.root.Link(target, trashed); err == nil {
		if err := l.root.Rename(pp, target); err != nil {
			l.root.Remove(trashed)
			return "", false, err
		}
		return target, true, nil
	}
	// No hard links on this disk (exFAT and the like): move the old file
	// aside, and back again if the new one can't take its place.
	if err := l.root.Rename(target, trashed); err != nil {
		return "", false, err
	}
	if err := l.root.Rename(pp, target); err != nil {
		if rerr := l.root.Rename(trashed, target); rerr != nil {
			// replaced reports that the old file is in the trash.
			return "", true, fmt.Errorf("%w (the old file is in the trash)", err)
		}
		return "", false, err
	}
	return target, true, nil
}

func (l *Local) AbortUpload(_ context.Context, up *UploadState) error {
	pp, err := partPath(up.ID)
	if err != nil {
		return err
	}
	if err := l.root.Remove(pp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// OrphanUploads lists staging files whose ID is not in known.
func (l *Local) OrphanUploads(known map[string]bool) []string {
	des, err := fs.ReadDir(l.root.FS(), uploadsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range des {
		if id := strings.TrimSuffix(de.Name(), ".part"); !known[id] {
			out = append(out, id)
		}
	}
	return out
}
