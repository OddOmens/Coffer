package files

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"time"
)

// WriteZip streams a zip of the named entries of dir to w. Entries are stored
// uncompressed: media is already compressed, and this keeps it fast.
func WriteZip(ctx context.Context, d Drive, w io.Writer, dir string, names []string, hideDot bool) error {
	return WriteZipFrom(ctx, w, []ZipSource{{Drive: d, Dir: dir, Names: names}}, hideDot)
}

// ZipSource is a set of entries of one folder, placed under Prefix in the zip.
type ZipSource struct {
	Drive  Drive
	Dir    string
	Names  []string
	Prefix string // folder inside the zip, "" for the top
}

// WriteZipFrom streams entries from several folders, possibly on different
// drives, into one zip.
func WriteZipFrom(ctx context.Context, w io.Writer, srcs []ZipSource, hideDot bool) error {
	zw := zip.NewWriter(w)
	for _, src := range srcs {
		d, dir := src.Drive, src.Dir
		for _, name := range src.Names {
			start := join(dir, name)
			err := d.Walk(ctx, start, func(e Entry) error {
				if e.Path != start && Hidden(e.Name, hideDot) {
					if e.IsDir {
						return fs.SkipDir
					}
					return nil
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, dir), "/")
				if dir == "." {
					rel = e.Path
				}
				if src.Prefix != "" {
					rel = src.Prefix + "/" + rel
				}
				// A name made outside Coffer may hold a backslash, which
				// Windows unzips as a separator (and "..\" as a way out).
				rel = strings.ReplaceAll(rel, `\`, "_")
				mod := time.Unix(e.ModTime, 0)
				if e.IsDir {
					_, err := zw.CreateHeader(&zip.FileHeader{Name: rel + "/", Modified: mod, Method: zip.Store})
					return err
				}
				// Open first: a file that vanished while zipping is left out
				// rather than added empty.
				in, _, err := d.Open(ctx, e.Path)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						return nil
					}
					return err
				}
				defer in.Close()
				hdr := &zip.FileHeader{Name: rel, Modified: mod, Method: zip.Store}
				hdr.SetMode(0o644)
				out, err := zw.CreateHeader(hdr)
				if err != nil {
					return err
				}
				_, err = io.Copy(out, in)
				return err
			})
			if err != nil {
				return err
			}
		}
	}
	return zw.Close()
}
