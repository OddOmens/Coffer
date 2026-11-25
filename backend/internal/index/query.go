package index

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"github.com/oddomens/coffer/internal/files"
)

// Scope limits a query to one folder of one drive.
type Scope struct {
	Drive string
	Root  string // drive-relative, "." for the whole drive
	Tag   string // passed back on hits (the place ID)
}

type Hit struct {
	files.Entry
	Tag string `json:"place"`
}

type SearchOptions struct {
	Query   string
	Kinds   []string
	HideDot bool
	Limit   int
}

func scopeWhere(sc Scope) (string, []any) {
	if sc.Root == "." || sc.Root == "" {
		return "e.drive = ? AND e.path != '.'", []any{sc.Drive}
	}
	lo, hi := prefixRange(sc.Root)
	return "e.drive = ? AND e.path >= ? AND e.path < ?", []any{sc.Drive, lo, hi}
}

func ftsQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

const hitCols = "e.path, e.name, e.is_dir, e.size, e.items, e.mtime, e.kind"

func scanHits(rows *sql.Rows, tag string) ([]Hit, error) {
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Path, &h.Name, &h.IsDir, &h.Size, &h.Items, &h.ModTime, &h.Kind); err != nil {
			return nil, err
		}
		if !h.IsDir {
			h.Mime = files.MimeType(h.Name)
		}
		h.Tag = tag
		out = append(out, h)
	}
	return out, rows.Err()
}

// Search finds names containing every word of the query, in each scope.
func (ix *Index) Search(ctx context.Context, scopes []Scope, o SearchOptions) ([]Hit, error) {
	words := strings.Fields(strings.TrimSpace(o.Query))
	if len(words) == 0 && len(o.Kinds) == 0 {
		return []Hit{}, nil
	}
	var fts []string
	var likes []string
	for _, w := range words {
		if utf8.RuneCountInString(w) >= 3 {
			fts = append(fts, ftsQuote(w))
		} else {
			likes = append(likes, "%"+likeEscape(w)+"%")
		}
	}
	out := []Hit{}
	for _, sc := range scopes {
		where, args := scopeWhere(sc)
		q := "SELECT " + hitCols + " FROM entries e"
		var qargs []any
		if len(fts) > 0 {
			q += " JOIN names n ON n.rowid = e.id AND names MATCH ?"
			qargs = append(qargs, strings.Join(fts, " "))
		}
		q += " WHERE " + where
		qargs = append(qargs, args...)
		for _, l := range likes {
			q += ` AND e.name LIKE ? ESCAPE '\'`
			qargs = append(qargs, l)
		}
		if o.HideDot {
			q += " AND e.hidden = 0"
		}
		if len(o.Kinds) > 0 {
			q += " AND e.kind IN (" + strings.TrimSuffix(strings.Repeat("?,", len(o.Kinds)), ",") + ")"
			for _, k := range o.Kinds {
				qargs = append(qargs, k)
			}
		}
		q += " ORDER BY (e.name = ? COLLATE NOCASE) DESC, e.is_dir DESC, length(e.name), e.mtime DESC LIMIT ?"
		qargs = append(qargs, strings.TrimSpace(o.Query), o.Limit)
		rows, err := ix.db.QueryContext(ctx, q, qargs...)
		if err != nil {
			return nil, err
		}
		hits, err := scanHits(rows, sc.Tag)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
		if len(out) >= o.Limit {
			return out[:o.Limit], nil
		}
	}
	return out, nil
}

// Recent returns the most recently modified files across scopes.
func (ix *Index) Recent(ctx context.Context, scopes []Scope, hideDot bool, limit int) ([]Hit, error) {
	out := []Hit{}
	for _, sc := range scopes {
		where, args := scopeWhere(sc)
		q := "SELECT " + hitCols + " FROM entries e WHERE e.is_dir = 0 AND " + where
		if hideDot {
			q += " AND e.hidden = 0"
		}
		rows, err := ix.db.QueryContext(ctx, q+" ORDER BY e.mtime DESC LIMIT ?", append(args, limit)...)
		if err != nil {
			return nil, err
		}
		hits, err := scanHits(rows, sc.Tag)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	// Merge newest first.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ModTime > out[j-1].ModTime; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type FolderSize struct {
	Size  int64
	Items int64
}

// FolderSizes returns the recursive size of each sub-folder of dir.
func (ix *Index) FolderSizes(ctx context.Context, drive, dir string) (map[string]FolderSize, error) {
	rows, err := ix.db.QueryContext(ctx, "SELECT path, size, items FROM entries WHERE drive = ? AND parent = ? AND is_dir = 1", drive, dir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FolderSize{}
	for rows.Next() {
		var p string
		var fsz FolderSize
		if err := rows.Scan(&p, &fsz.Size, &fsz.Items); err != nil {
			return nil, err
		}
		out[p] = fsz
	}
	return out, rows.Err()
}

// Size returns the indexed size of a folder.
func (ix *Index) Size(ctx context.Context, drive, p string) (FolderSize, bool) {
	var f FolderSize
	err := ix.db.QueryRowContext(ctx, "SELECT size, items FROM entries WHERE drive = ? AND path = ?", drive, p).Scan(&f.Size, &f.Items)
	return f, err == nil
}

type KindStat struct {
	Kind  string `json:"kind"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
}

// Breakdown sums file counts and bytes per kind below root.
func (ix *Index) Breakdown(ctx context.Context, sc Scope) ([]KindStat, error) {
	where, args := scopeWhere(sc)
	rows, err := ix.db.QueryContext(ctx, "SELECT e.kind, COUNT(*), COALESCE(SUM(e.size), 0) FROM entries e WHERE e.is_dir = 0 AND "+
		where+" GROUP BY e.kind ORDER BY 3 DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KindStat{}
	for rows.Next() {
		var k KindStat
		if err := rows.Scan(&k.Kind, &k.Files, &k.Bytes); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type ScanInfo struct {
	Drive    string `json:"drive"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished"`
	Files    int64  `json:"files"`
	Dirs     int64  `json:"dirs"`
	Bytes    int64  `json:"bytes"`
	Ms       int64  `json:"ms"`
	Error    string `json:"error"`
	Running  bool   `json:"running"`
}

func (ix *Index) ScanInfo(ctx context.Context, drive string) ScanInfo {
	si := ScanInfo{Drive: drive}
	_ = ix.db.QueryRowContext(ctx, "SELECT started, finished, files, dirs, bytes, ms, error FROM scans WHERE drive = ?", drive).
		Scan(&si.Started, &si.Finished, &si.Files, &si.Dirs, &si.Bytes, &si.Ms, &si.Error)
	si.Running = ix.Scanning(drive)
	// Live totals reflect incremental changes since the last scan.
	var size, items int64
	if ix.db.QueryRowContext(ctx, "SELECT size, items FROM entries WHERE drive = ? AND path = '.'", drive).Scan(&size, &items) == nil {
		si.Bytes, si.Files = size, items
	}
	return si
}
