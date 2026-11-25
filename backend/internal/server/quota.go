package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/store"
)

// Storage limits. A user's limit covers their home folders; anything added
// below one of them counts, whoever adds it (the owner, someone they shared
// with, or a link's uploader).

// quotaUsed sums the indexed size of u's home folders.
func (s *Server) quotaUsed(ctx context.Context, u *store.User) int64 {
	ps, err := s.store.PlacesForUser(ctx, u.ID)
	if err != nil {
		return 0
	}
	var used int64
	for _, p := range ps {
		if p.GrantedBy != 0 {
			continue
		}
		if fsz, ok := s.index.Size(ctx, p.DriveID, p.Path); ok {
			used += fsz.Size
		}
	}
	return used
}

// limitedOwners lists the users with a storage limit whose home folders hold
// dir on drive. With src set (a move on the same drive), owners whose homes
// already hold src are left out: the move doesn't grow them.
func (s *Server) limitedOwners(ctx context.Context, drive, src, dir string) []*store.User {
	homes, err := s.store.HomePlacesOn(ctx, drive)
	if err != nil {
		return nil
	}
	seen := map[int64]bool{}
	if src != "" {
		for _, p := range homes {
			if files.Within(p.Path, src) {
				seen[p.UserID] = true
			}
		}
	}
	var out []*store.User
	for _, p := range homes {
		if seen[p.UserID] || !files.Within(p.Path, dir) {
			continue
		}
		seen[p.UserID] = true
		if u, err := s.store.UserByID(ctx, p.UserID); err == nil && u.QuotaBytes > 0 {
			out = append(out, u)
		}
	}
	return out
}

// quotaCheck reports an error when adding n bytes below dir on drive would
// take a limited user over their limit.
func (s *Server) quotaCheck(ctx context.Context, drive, dir string, n int64) error {
	return s.quotaCheckMove(ctx, drive, "", dir, n)
}

// quotaCheckMove is quotaCheck for moving src, n bytes on the same drive,
// below dir.
func (s *Server) quotaCheckMove(ctx context.Context, drive, src, dir string, n int64) error {
	for _, u := range s.limitedOwners(ctx, drive, src, dir) {
		if used := s.quotaUsed(ctx, u); used+n > u.QuotaBytes {
			return &quotaError{owner: u.Username, left: max(0, u.QuotaBytes-used)}
		}
	}
	return nil
}

// quotaCheckOpen is quotaCheck for starting an upload: the declared sizes
// of uploads still open below a limited user's home folders count too, so
// many uploads started side by side can't each claim the same free space.
func (s *Server) quotaCheckOpen(ctx context.Context, drive, dir string, n int64) error {
	byDrive := map[string][]*store.Upload{}
	for _, u := range s.limitedOwners(ctx, drive, "", dir) {
		ps, _ := s.store.PlacesForUser(ctx, u.ID)
		var open int64
		for _, p := range ps {
			if p.GrantedBy != 0 {
				continue
			}
			ups, ok := byDrive[p.DriveID]
			if !ok {
				ups, _ = s.store.UploadsOn(ctx, p.DriveID)
				byDrive[p.DriveID] = ups
			}
			for _, up := range ups {
				if files.Within(p.Path, up.Dir) {
					open += up.Size
				}
			}
		}
		if used := s.quotaUsed(ctx, u) + open; used+n > u.QuotaBytes {
			return &quotaError{owner: u.Username, left: max(0, u.QuotaBytes-used)}
		}
	}
	return nil
}

// openUploadBytes sums the declared sizes of the uploads still open on a
// drive: space they will take that free space doesn't show yet.
func (s *Server) openUploadBytes(ctx context.Context, drive string) int64 {
	ups, err := s.store.UploadsOn(ctx, drive)
	if err != nil {
		return 0
	}
	var n int64
	for _, up := range ups {
		n += up.Size
	}
	return n
}

// quotaLeft returns how many bytes may still be added below dir on drive,
// or -1 when no limit applies.
func (s *Server) quotaLeft(ctx context.Context, drive, dir string) int64 {
	left := int64(-1)
	for _, u := range s.limitedOwners(ctx, drive, "", dir) {
		if l := max(0, u.QuotaBytes-s.quotaUsed(ctx, u)); left < 0 || l < left {
			left = l
		}
	}
	return left
}

// sizeOf is the size of p (a file or a whole folder) on m, from the index
// when it has it, so copies and moves of unindexed items are still counted.
func (s *Server) sizeOf(ctx context.Context, m *mounted, p string) int64 {
	if fsz, ok := s.index.Size(ctx, m.info.ID, p); ok {
		return fsz.Size
	}
	e, err := m.d.Stat(ctx, p)
	if err != nil || !e.IsDir {
		return e.Size
	}
	var n int64
	_ = m.d.Walk(ctx, p, func(e files.Entry) error {
		n += e.Size
		return nil
	})
	return n
}

type quotaError struct {
	owner string
	left  int64
}

func (e *quotaError) Error() string {
	return fmt.Sprintf("this would go over %s's storage limit (%s left)", e.owner, humanBytes(e.left))
}

func (s *Server) quotaFail(w http.ResponseWriter, err error) {
	writeErr(w, http.StatusInsufficientStorage, "quota", err.Error())
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
