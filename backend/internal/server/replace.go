package server

import (
	"context"
	"io"
	"path"
	"strings"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// Saving over a file from WebDAV or the text editor works like a web upload
// that replaces one: it needs permission to delete there, and the old
// version goes to the trash first, so it can be restored.

// finderScratch matches the metadata files macOS writes next to others. They
// are rewritten all the time and hold nothing worth keeping.
func finderScratch(name string) bool {
	return strings.HasPrefix(name, "._") || name == ".DS_Store"
}

// replaces reports whether writing p would replace a file worth keeping: one
// with content. Finder creates an empty file before writing it, and that
// first save mustn't need delete rights.
func replaces(ctx context.Context, d files.Drive, p string) (files.Entry, bool) {
	e, err := d.Stat(ctx, p)
	return e, err == nil && !e.IsDir && e.Size > 0 && !finderScratch(path.Base(p))
}

// replaceFile stores size bytes from body at p, moving a file it replaces to
// the trash first. The caller has checked the delete permission.
func (s *Server) replaceFile(ctx context.Context, u *store.User, m *mounted, p string, body io.Reader, size int64) error {
	id := ""
	if e, ok := replaces(ctx, m.d, p); ok {
		id = security.RandomToken(12)
		if _, _, err := m.d.Trash(ctx, p, id); err != nil {
			return err
		}
		item := &store.TrashItem{ID: id, DriveID: m.info.ID, OrigPath: p, Size: e.Size}
		if u != nil {
			item.UserID = u.ID
		}
		if err := s.store.AddTrash(ctx, item); err != nil {
			_, _ = m.d.Restore(ctx, id, p)
			return err
		}
	}
	if err := m.d.WriteFile(ctx, p, body, size); err != nil {
		if id != "" {
			// Put the old version back where it was.
			if _, rerr := m.d.Restore(ctx, id, p); rerr == nil {
				_ = s.store.DeleteTrash(ctx, id)
			}
		}
		return err
	}
	return nil
}
