package backup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"

	"github.com/oddomens/coffer/internal/files"
)

// Coffer's own database and key, copied next to the snapshots so Coffer
// itself can be restored:
//
//	_coffer/<snapshot name>/coffer.db
//	_coffer/<snapshot name>/secret.key
//
// They are kept apart from the snapshot manifests, so restoring files never
// brings them back into a drive, and only the newest SelfKeep are kept.

const (
	SelfDir  = files.SelfCopyDir
	SelfKeep = 7
)

// WriteSelfCopy uploads the local files in srcs (name in the copy -> path on
// this machine) to dst/dstRoot/_coffer/<snap>/, then removes older copies
// beyond SelfKeep.
func WriteSelfCopy(ctx context.Context, dst files.Drive, dstRoot, snap string, srcs map[string]string) error {
	if !validName(snap) {
		return errors.New("invalid snapshot name")
	}
	dir := join(dstRoot, SelfDir+"/"+snap)
	if err := dst.MkdirAll(ctx, dir); err != nil {
		return err
	}
	names := make([]string, 0, len(srcs))
	for name := range srcs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := uploadLocal(ctx, srcs[name], dst, dir+"/"+name); err != nil {
			return err
		}
	}
	_, err := pruneSelf(ctx, dst, dstRoot, SelfKeep)
	return err
}

func uploadLocal(ctx context.Context, src string, dst files.Drive, p string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return dst.WriteFile(ctx, p, f, info.Size())
}

// SelfCopies lists the snapshot names that have a copy of Coffer itself,
// oldest first.
func SelfCopies(ctx context.Context, dst files.Drive, dstRoot string) ([]string, error) {
	es, err := dst.List(ctx, join(dstRoot, SelfDir), false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if e.IsDir && validName(e.Name) {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func pruneSelf(ctx context.Context, dst files.Drive, dstRoot string, keep int) (int, error) {
	names, err := SelfCopies(ctx, dst, dstRoot)
	if err != nil || len(names) <= keep {
		return 0, err
	}
	n := 0
	for _, name := range names[:len(names)-keep] {
		if err := dst.RemoveAll(ctx, join(dstRoot, SelfDir+"/"+name)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
