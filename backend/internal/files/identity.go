package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
)

// A local drive's folder carries a marker naming the drive, so a disk that
// isn't mounted (leaving its empty mount point on the system disk) is seen
// as offline instead of as a drive whose files were all deleted.

const driveMarker = InternalDir + "/drive-id"

var (
	// ErrNotMounted: the folder has no marker and nothing in it, but the
	// drive had files: most likely the disk isn't mounted.
	ErrNotMounted = errors.New("the drive's folder is empty and unmarked; is the disk plugged in and mounted?")
	// ErrWrongDrive: the folder is marked for other drives only.
	ErrWrongDrive = errors.New("the folder holds a different drive (its .coffer/drive-id marker doesn't match)")
	// ErrRemounted: the path now leads to another folder than the one
	// opened, such as a disk mounted again. Opening it afresh fixes it.
	ErrRemounted = errors.New("the drive's folder was mounted again")
)

// OpenLocalDrive opens dir as the drive id. A folder without a marker gets
// one when it holds anything, or when adoptEmpty says an empty folder is
// expected (a new drive, or one whose index had no files). Otherwise it
// isn't opened: it is probably an unmounted disk's mount point. shared
// lists other drives set up on the same folder: a marker naming one of
// them takes this one in too.
func OpenLocalDrive(dir, id string, adoptEmpty bool, shared []string) (*Local, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	ids, err := readMarker(root)
	switch {
	case err == nil && !slices.Contains(ids, id):
		if slices.ContainsFunc(ids, func(o string) bool { return slices.Contains(shared, o) }) {
			err = writeMarker(root, append(ids, id))
		} else {
			err = ErrWrongDrive
		}
	case errors.Is(err, fs.ErrNotExist):
		err = nil
		if !adoptEmpty {
			var empty bool
			if empty, err = isEmpty(root); err == nil && empty {
				err = ErrNotMounted
			}
		}
		if err == nil {
			err = writeMarker(root, append(ids, id))
		}
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return openLocalRoot(root, dir)
}

// MarkLocalDrive marks dir as (also) holding the drive id, for a folder an
// admin just picked.
func MarkLocalDrive(dir, id string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	ids, err := readMarker(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if slices.Contains(ids, id) {
		return nil
	}
	return writeMarker(root, append(ids, id))
}

// Check confirms the drive is still there: the folder's path still leads to
// the folder that was opened, and its marker still names id. It reads the
// path afresh, so it notices a disk that was unplugged or unmounted.
func (l *Local) Check(id string) error {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	ids, err := readMarker(root)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotMounted
	}
	if err != nil {
		return err
	}
	if !slices.Contains(ids, id) {
		return ErrWrongDrive
	}
	now, err := root.Stat(".")
	if err != nil {
		return err
	}
	was, err := l.root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(now, was) {
		return ErrRemounted
	}
	return nil
}

// readMarker returns the drive IDs in the marker, one per line. Two drives
// may share a folder.
func readMarker(root *os.Root) ([]string, error) {
	b, err := root.ReadFile(driveMarker)
	if err != nil {
		return nil, err
	}
	var ids []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}

func writeMarker(root *os.Root, ids []string) error {
	if err := root.MkdirAll(InternalDir, 0o770); err != nil {
		return err
	}
	tmp := driveMarker + ".tmp-" + tmpSuffix()
	if err := root.WriteFile(tmp, []byte(strings.Join(ids, "\n")+"\n"), 0o660); err != nil {
		root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, driveMarker); err != nil {
		root.Remove(tmp)
		return err
	}
	return nil
}

// isEmpty reports whether the folder holds nothing but Coffer's own folder
// (which an older Coffer may have made in an empty mount point).
func isEmpty(root *os.Root) (bool, error) {
	d, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer d.Close()
	for {
		names, err := d.Readdirnames(16)
		for _, n := range names {
			if !isInternal(n) {
				return false, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
	}
}
