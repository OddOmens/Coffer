package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// withSizes fills in recursive folder sizes from the index. Paths stay
// drive paths.
func (s *Server) withSizes(ctx context.Context, a *access, dir string, es []files.Entry) []files.Entry {
	sizes, _ := s.index.FolderSizes(ctx, a.DriveID, dir)
	for i := range es {
		if es[i].IsDir {
			if fsz, ok := sizes[es[i].Path]; ok {
				es[i].Size, es[i].Items = fsz.Size, fsz.Items
			} else {
				es[i].Size = -1 // not indexed yet
			}
		}
	}
	return es
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// "Show hidden files" in a user's own settings only changes their listing,
	// never what share-link visitors see.
	hide := s.cur().HideDotfiles && !prefBool(u.Prefs, "showHidden", false)
	es, err := a.d().List(r.Context(), p, hide)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	es = a.visible(es)
	var folder index.FolderSize
	folder, _ = s.index.Size(r.Context(), a.DriveID, p)
	covers := s.backupCovers(r.Context(), a.DriveID)
	writeJSON(w, http.StatusOK, map[string]any{
		"path": a.rel(p), "entries": s.decorate(r.Context(), u, a, p, s.withSizes(r.Context(), a, p, es), covers), "place": a,
		"size": folder.Size, "items": folder.Items, "backup": markFor(covers, p, true, 0, 0),
		"backupStore": s.backupStoreAt(r.Context(), u, a.DriveID, p),
	})
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := a.d().Stat(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if e.IsDir {
		if fsz, ok := s.index.Size(r.Context(), a.DriveID, p); ok {
			e.Size, e.Items = fsz.Size, fsz.Items
		}
	}
	e.Path = a.rel(p)
	writeJSON(w, http.StatusOK, e)
}

// sendFile serves p from a drive, redirecting S3 attachment downloads to a
// presigned URL when the drive allows it.
func (s *Server) sendFile(w http.ResponseWriter, r *http.Request, m *mounted, p string, inline bool) {
	if s3, ok := m.d.(*files.S3); ok && !inline && s3.Config().DirectDownloads && r.Header.Get("Range") == "" {
		if u, err := s3.PresignedGet(r.Context(), p, path.Base(p)); err == nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}
	f, e, err := m.d.Open(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	serveFile(w, r, f, e, path.Base(p), inline)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.sendFile(w, r, a.m, p, r.URL.Query().Get("inline") == "1")
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.serveThumbFor(w, r, a.m, p)
}

// streamZip zips the named entries of dir, or all of dir when names is empty.
// A named symlink is followed, so each name must stay inside the place.
func (s *Server) streamZip(w http.ResponseWriter, r *http.Request, a *access, dir string, names []string) {
	d := a.d()
	hide := s.cur().HideDotfiles
	zipName := "download"
	if dir != "." {
		zipName = path.Base(dir)
	}
	if len(names) == 0 {
		es, err := d.List(r.Context(), dir, hide)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, e := range a.visible(es) {
			names = append(names, e.Name)
		}
	} else {
		if len(names) > 5000 {
			badRequest(w, "too many items")
			return
		}
		for _, n := range names {
			if !files.ValidEntryName(n) {
				s.fail(w, r, files.ErrInvalidName)
				return
			}
			if _, err := a.resolve(a.rel(path.Join(dir, n))); err != nil {
				s.fail(w, r, err)
				return
			}
			if !d.Exists(r.Context(), path.Join(dir, n)) {
				s.fail(w, r, fs.ErrNotExist)
				return
			}
		}
		if len(names) == 1 {
			zipName = names[0]
		}
	}
	zipHeaders(w, zipName)
	if err := files.WriteZip(r.Context(), d, w, dir, names, hide); err != nil && r.Context().Err() == nil {
		s.log.Warn("zip stream failed", "err", err)
		// The status line is gone; cut the connection so the zip doesn't
		// look complete.
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) handleZip(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	dir, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.streamZip(w, r, a, dir, r.URL.Query()["name"])
}

type hitJSON struct {
	files.Entry
	Place     string `json:"place"`
	PlaceName string `json:"placeName"`
}

func (s *Server) hitsOut(ps []*access, hits []index.Hit) []hitJSON {
	out := make([]hitJSON, 0, len(hits))
	for _, h := range hits {
		a, ok := placeOf(ps, h)
		if !ok {
			continue
		}
		e := h.Entry
		e.Path = a.rel(e.Path)
		out = append(out, hitJSON{Entry: e, Place: a.ID, PlaceName: a.Name})
	}
	return out
}

// handleSearch answers from the index, so results arrive as you type.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, u *store.User) {
	q := r.URL.Query()
	ps, err := s.placesFor(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sc := scopes(ps)
	if id := q.Get("place"); id != "" {
		a, ok := s.place(w, r, u, id)
		if !ok {
			return
		}
		root, err := a.resolve(q.Get("path")) // narrow to a folder inside the place
		if err != nil {
			s.fail(w, r, err)
			return
		}
		ps = []*access{a}
		sc = []index.Scope{{Drive: a.DriveID, Root: root, Tag: a.ID}}
	}
	var kinds []string
	if k := q.Get("kinds"); k != "" {
		kinds = strings.Split(k, ",")
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	hits, err := s.index.Search(ctx, sc, index.SearchOptions{
		Query: q.Get("q"), Kinds: kinds, HideDot: s.cur().HideDotfiles, Limit: limit,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.hitsOut(ps, hits), "ms": time.Since(start).Milliseconds()})
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request, u *store.User) {
	ps, err := s.placesFor(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	hits, err := s.index.Recent(r.Context(), scopes(ps), s.cur().HideDotfiles, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.hitsOut(ps, hits)})
}

func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	ks, err := s.index.Breakdown(r.Context(), index.Scope{Drive: a.DriveID, Root: a.Root})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ks)
}

func requireWrite(w http.ResponseWriter, a *access) bool {
	if !a.CanWrite {
		forbidden(w, "you have read-only access here")
		return false
	}
	return true
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place  string `json:"place"`
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	if err := files.ValidName(req.Name); err != nil {
		s.fail(w, r, err)
		return
	}
	parent, err := a.resolve(req.Parent)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := path.Join(parent, req.Name)
	if err := a.d().Mkdir(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	s.reindex(a.m, parent)
	s.audit(r, u, "", "folder.create", a.DriveID+":"+p, "")
	e, _ := a.d().Stat(r.Context(), p)
	e.Path = a.rel(p)
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place string `json:"place"`
		Path  string `json:"path"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	if err := files.ValidName(req.Name); err != nil {
		s.fail(w, r, err)
		return
	}
	src, err := a.resolve(req.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if src == a.Root {
		forbidden(w, "the top folder cannot be renamed here")
		return
	}
	dst := path.Join(files.Parent(src), req.Name)
	if !s.checkUnlocked(w, r, u, a.DriveID, src) {
		return
	}
	if err := a.d().Rename(r.Context(), src, dst); err != nil {
		s.fail(w, r, err)
		return
	}
	s.afterMove(a.m, src, dst)
	s.audit(r, u, "", "rename", a.DriveID+":"+src, "to "+dst)
	writeJSON(w, http.StatusOK, map[string]string{"path": a.rel(dst)})
}

func (s *Server) afterMove(m *mounted, src, dst string) {
	ctx := context.Background()
	_ = s.store.Repath(ctx, m.info.ID, src, dst)
	if err := s.index.Move(ctx, m.info.ID, src, dst); err != nil {
		s.log.Warn("index move failed", "err", err)
	}
}

type batchResult struct {
	Done   int          `json:"done"`
	Errors []batchError `json:"errors"`
}

type batchError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func errText(err error) string {
	switch {
	case errors.Is(err, files.ErrExists):
		return "an item with that name already exists there"
	case errors.Is(err, files.ErrIntoSelf):
		return err.Error()
	case errors.Is(err, files.ErrInvalidPath), errors.Is(err, files.ErrInvalidName):
		return "invalid path"
	case errors.Is(err, fs.ErrNotExist):
		return "not found"
	case errors.Is(err, errForbidden):
		return "not allowed"
	case errors.Is(err, errLocked):
		return err.Error()
	case errors.As(err, new(*quotaError)):
		return err.Error()
	case driveGone(err):
		return "the drive is offline"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "the drive is full"
	default:
		return "failed"
	}
}

var errForbidden = errors.New("forbidden")

func (s *Server) batch(a *access, paths []string, fn func(src string) error) batchResult {
	res := batchResult{Errors: []batchError{}}
	for _, p := range paths {
		src, err := a.resolve(p)
		if err == nil && src == a.Root {
			err = files.ErrInvalidPath
		}
		if err == nil {
			err = fn(src)
		}
		if err != nil {
			if !errors.Is(err, files.ErrExists) && !errors.Is(err, files.ErrIntoSelf) && !errors.Is(err, fs.ErrNotExist) {
				s.log.Warn("batch op failed", "err", err)
			}
			res.Errors = append(res.Errors, batchError{Path: p, Error: errText(err)})
			continue
		}
		res.Done++
	}
	return res
}

type moveReq struct {
	Place     string   `json:"place"`
	Paths     []string `json:"paths"`
	DestPlace string   `json:"destPlace"`
	Dest      string   `json:"dest"`
}

// copyAcross copies src (a file or folder tree) between two drives.
func copyAcross(ctx context.Context, from files.Drive, src string, to files.Drive, dst string) error {
	if to.Exists(ctx, dst) {
		return files.ErrExists
	}
	return from.Walk(ctx, src, func(e files.Entry) error {
		target := dst + strings.TrimPrefix(e.Path, src)
		if e.IsDir {
			return to.MkdirAll(ctx, target)
		}
		rc, info, err := from.Open(ctx, e.Path)
		if err != nil {
			return err
		}
		defer rc.Close()
		return to.WriteFile(ctx, target, rc, info.Size)
	})
}

func (s *Server) moveOrCopy(w http.ResponseWriter, r *http.Request, u *store.User, move bool) {
	var req moveReq
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok {
		return
	}
	if move && !requireWrite(w, a) {
		return
	}
	b := a
	if req.DestPlace != "" && req.DestPlace != req.Place {
		if b, ok = s.place(w, r, u, req.DestPlace); !ok {
			return
		}
	}
	if !requireWrite(w, b) {
		return
	}
	dest, err := b.resolve(req.Dest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Moving something out of a place removes it from there, like a delete.
	if move && !a.CanDelete && (b.DriveID != a.DriveID || !files.Within(a.Root, dest)) {
		forbidden(w, "moving out of this folder needs delete rights here")
		return
	}
	ctx := r.Context()
	srcParents := map[string]bool{}
	res := s.batch(a, req.Paths, func(src string) error {
		name := path.Base(src)
		if move {
			if err := s.lockedErr(ctx, u, a.DriveID, src); err != nil {
				return err
			}
		}
		// Moves count too: they can carry files into a limited home folder.
		if move && b.DriveID == a.DriveID {
			if err := s.quotaCheckMove(ctx, b.DriveID, src, dest, s.sizeOf(ctx, a.m, src)); err != nil {
				return err
			}
		} else if err := s.quotaCheck(ctx, b.DriveID, dest, s.sizeOf(ctx, a.m, src)); err != nil {
			return err
		}
		switch {
		case b.DriveID == a.DriveID && move:
			dst := path.Join(dest, name)
			if err := a.d().Rename(ctx, src, dst); err != nil {
				return err
			}
			s.afterMove(a.m, src, dst)
			s.audit(r, u, "", "move", a.DriveID+":"+src, "to "+dst)
		case b.DriveID == a.DriveID:
			dst := path.Join(dest, files.UniqueName(ctx, b.d(), dest, name))
			if err := a.d().Copy(ctx, src, dst); err != nil {
				return err
			}
			s.audit(r, u, "", "copy", a.DriveID+":"+src, "to "+dst)
		default:
			dst := path.Join(dest, name)
			if !move {
				dst = path.Join(dest, files.UniqueName(ctx, b.d(), dest, name))
			}
			if err := copyAcross(ctx, a.d(), src, b.d(), dst); err != nil {
				return err
			}
			if move {
				if err := s.trash(r, u, a.m, src); err != nil {
					return err
				}
				srcParents[files.Parent(src)] = true
			}
			s.audit(r, u, "", map[bool]string{true: "move", false: "copy"}[move], a.DriveID+":"+src, "to "+b.DriveID+":"+dst)
		}
		return nil
	})
	s.reindex(b.m, dest)
	for p := range srcParents {
		s.reindex(a.m, p)
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.moveOrCopy(w, r, u, true)
}

func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.moveOrCopy(w, r, u, false)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place     string   `json:"place"`
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok {
		return
	}
	if !a.CanDelete {
		forbidden(w, "you are not allowed to delete here")
		return
	}
	writeJSON(w, http.StatusOK, s.batch(a, req.Paths, func(src string) error {
		if err := s.lockedErr(r.Context(), u, a.DriveID, src); err != nil {
			return err
		}
		if req.Permanent {
			if err := a.d().RemoveAll(r.Context(), src); err != nil {
				return err
			}
			_ = s.index.Remove(r.Context(), a.DriveID, src)
			s.audit(r, u, "", "delete.permanent", a.DriveID+":"+src, "")
			return nil
		}
		return s.trash(r, u, a.m, src)
	}))
}

func (s *Server) trash(r *http.Request, u *store.User, m *mounted, p string) error {
	id := security.RandomToken(12)
	ctx := r.Context()
	isDir, size, err := m.d.Trash(ctx, p, id)
	if err != nil {
		return err
	}
	if isDir {
		if fsz, ok := s.index.Size(ctx, m.info.ID, p); ok {
			size = fsz.Size
		}
	}
	uid := int64(0)
	if u != nil {
		uid = u.ID
	}
	if err := s.store.AddTrash(ctx, &store.TrashItem{ID: id, DriveID: m.info.ID, UserID: uid, OrigPath: p, IsDir: isDir, Size: size}); err != nil {
		_, _ = m.d.Restore(ctx, id, p)
		return err
	}
	_ = s.index.Remove(ctx, m.info.ID, p)
	s.audit(r, u, "", "delete", m.info.ID+":"+p, "")
	return nil
}

// Text editing.

const maxTextSize = 8 << 20

func etagOf(e files.Entry) string { return fmt.Sprintf("%d-%d", e.ModTime, e.Size) }

func (s *Server) handleTextGet(w http.ResponseWriter, r *http.Request, u *store.User) {
	a, ok := s.place(w, r, u, "")
	if !ok {
		return
	}
	p, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, e, err := a.d().Open(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	if e.Size > maxTextSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "this file is too large to edit here (8 MB max)")
		return
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTextSize+1))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !utf8.Valid(b) {
		writeErr(w, http.StatusUnsupportedMediaType, "binary", "this file is not text")
		return
	}
	// Saving replaces the file, which needs delete rights too.
	writeJSON(w, http.StatusOK, map[string]any{"content": string(b), "etag": etagOf(e), "size": e.Size, "modTime": e.ModTime,
		"writable": a.CanWrite && a.CanDelete})
}

func (s *Server) handleTextPut(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place   string `json:"place"`
		Path    string `json:"path"`
		Content string `json:"content"`
		Etag    string `json:"etag"`
		Create  bool   `json:"create"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTextSize+64<<10))
	if err := dec.Decode(&req); err != nil {
		badRequest(w, "invalid request or file too large")
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil || p == a.Root {
		s.fail(w, r, files.ErrInvalidPath)
		return
	}
	if err := files.ValidName(path.Base(p)); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.checkUnlocked(w, r, u, a.DriveID, p) {
		return
	}
	cur, statErr := a.d().Stat(r.Context(), p)
	switch {
	case req.Create && statErr == nil:
		s.fail(w, r, files.ErrExists)
		return
	case !req.Create && statErr != nil:
		s.fail(w, r, statErr)
		return
	case !req.Create && cur.IsDir:
		s.fail(w, r, files.ErrIsDir)
		return
	case !req.Create && req.Etag != etagOf(cur):
		writeErr(w, http.StatusConflict, "changed", "this file changed since you opened it")
		return
	case !req.Create && !a.CanDelete:
		forbidden(w, "you are not allowed to replace files here")
		return
	}
	if grow := int64(len(req.Content)) - cur.Size; grow > 0 {
		if err := s.quotaCheck(r.Context(), a.DriveID, files.Parent(p), grow); err != nil {
			s.quotaFail(w, err)
			return
		}
	}
	// The previous version goes to the trash, like a replaced upload.
	if err := s.replaceFile(r.Context(), u, a.m, p, strings.NewReader(req.Content), int64(len(req.Content))); err != nil {
		s.fail(w, r, err)
		return
	}
	s.reindex(a.m, files.Parent(p))
	e, _ := a.d().Stat(r.Context(), p)
	action := "edit"
	if req.Create {
		action = "file.create"
	}
	s.audit(r, u, "", action, a.DriveID+":"+p, fmt.Sprintf("%d bytes", len(req.Content)))
	writeJSON(w, http.StatusOK, map[string]any{"etag": etagOf(e), "path": a.rel(p), "modTime": e.ModTime, "size": e.Size})
}

// Uploads. The protocol is shared with public share links:
//   POST   create {place, dir, name, size, overwrite} -> {id, offset, chunkSize}
//   PATCH  {id} with Upload-Offset header and raw bytes -> {offset}
//   GET    {id} -> {offset, size}
//   POST   {id}/complete -> entry
//   DELETE {id}

const (
	defaultChunk = 16 << 20
	maxChunkSize = 64 << 20
)

type uploadReq struct {
	Place     string `json:"place"`
	Dir       string `json:"dir"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Overwrite bool   `json:"overwrite"`
	Uploader  string `json:"uploader"`
	// SkipExisting: if the file is already there, leave it and say so
	// (re-uploading a folder to fill in what's missing).
	SkipExisting bool `json:"skipExisting"`
}

func uploadState(up *store.Upload) *files.UploadState {
	st := &files.UploadState{}
	_ = json.Unmarshal([]byte(up.State), st)
	st.ID, st.Size, st.Dir, st.Name, st.Overwrite = up.ID, up.Size, up.Dir, up.Name, up.Overwrite
	return st
}

func (s *Server) saveState(ctx context.Context, up *store.Upload, st *files.UploadState) error {
	b, _ := json.Marshal(files.UploadState{Target: st.Target, Remote: st.Remote, Parts: st.Parts, KeySalt: st.KeySalt, KeyCheck: st.KeyCheck})
	up.State = string(b)
	return s.store.SaveUploadState(ctx, up.ID, up.State)
}

// startUpload opens an upload session and reports whether it did. Uploads
// already open count against free space and storage limits by their full
// declared size.
func (s *Server) startUpload(w http.ResponseWriter, r *http.Request, m *mounted, up *store.Upload, maxSize int64) bool {
	if err := files.ValidRelName(up.Name); err != nil {
		s.fail(w, r, err)
		return false
	}
	// The name may hold sub-folders (a dropped folder): a symlink among them
	// mustn't lead the file out of the folder it's uploaded into.
	if _, err := files.Confine(m.d, up.Dir, up.Name); err != nil {
		s.fail(w, r, err)
		return false
	}
	if up.Size < 0 || (maxSize > 0 && up.Size > maxSize) {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("files can be at most %d MB here", maxSize>>20))
		return false
	}
	if free, _, ok := m.d.Usage(r.Context()); ok && up.Size+s.openUploadBytes(r.Context(), m.info.ID)+(1<<30) > free {
		writeErr(w, http.StatusInsufficientStorage, "no_space", "not enough free space on the drive")
		return false
	}
	if e, err := m.d.Stat(r.Context(), up.Dir); err != nil || !e.IsDir {
		s.fail(w, r, files.ErrNotDir)
		return false
	}
	if err := s.quotaCheckOpen(r.Context(), m.info.ID, up.Dir, up.Size); err != nil {
		s.quotaFail(w, err)
		return false
	}
	up.ID = security.RandomToken(18)
	up.DriveID = m.info.ID
	st := uploadState(up)
	if err := m.d.StartUpload(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return false
	}
	b, _ := json.Marshal(files.UploadState{Target: st.Target, Remote: st.Remote})
	up.State = string(b)
	if err := s.store.CreateUpload(r.Context(), up); err != nil {
		_ = m.d.AbortUpload(r.Context(), st)
		s.fail(w, r, err)
		return false
	}
	chunk := m.d.ChunkSize()
	if chunk == 0 {
		chunk = defaultChunk
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": up.ID, "offset": 0, "chunkSize": chunk})
	return true
}

func (s *Server) loadUpload(w http.ResponseWriter, r *http.Request, owns func(*store.Upload) bool) (*store.Upload, *mounted, bool) {
	up, err := s.store.UploadByID(r.Context(), r.PathValue("id"))
	if err != nil || !owns(up) {
		writeErr(w, http.StatusNotFound, "not_found", "upload not found")
		return nil, nil, false
	}
	m, err := s.drive(up.DriveID)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	return up, m, true
}

func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request, up *store.Upload, m *mounted) {
	off, err := m.d.UploadOffset(r.Context(), uploadState(up))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"offset": off, "size": up.Size})
}

func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request, up *store.Upload, m *mounted) {
	off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || off < 0 {
		badRequest(w, "missing Upload-Offset header")
		return
	}
	if r.ContentLength > maxChunkSize {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "chunk too large")
		return
	}
	if r.ContentLength < 0 && m.d.ChunkSize() > 0 {
		badRequest(w, "Content-Length is required")
		return
	}
	st := uploadState(up)
	body := http.MaxBytesReader(w, r.Body, maxChunkSize)
	n, err := m.d.WriteChunk(r.Context(), st, off, r.ContentLength, body)
	if n > off {
		s.mon.BytesIn.Add(n - off)
	}
	if len(st.Parts) > 0 {
		_ = s.saveState(context.WithoutCancel(r.Context()), up, st)
	}
	switch {
	case errors.Is(err, files.ErrOffset):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "offset mismatch", "code": "offset", "offset": n})
	case errors.Is(err, files.ErrChunkSize):
		badRequest(w, fmt.Sprintf("chunks must be %d bytes except the last", m.d.ChunkSize()))
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]int64{"offset": n})
	}
}

func (s *Server) finishUpload(w http.ResponseWriter, r *http.Request, up *store.Upload, m *mounted, u *store.User) (string, bool) {
	// Check the limit again: uploads started side by side were each checked
	// against the same usage, and earlier ones may have finished since.
	if err := s.quotaCheck(r.Context(), m.info.ID, up.Dir, up.Size); err != nil {
		s.quotaFail(w, err)
		return "", false
	}
	// A disk unplugged since the upload began: don't finish into its empty
	// mount point.
	m, err := s.checkDrive(r.Context(), m.info.ID)
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	// A symlink put among the name's folders since the upload began.
	if _, err := files.Confine(m.d, up.Dir, up.Name); err != nil {
		s.fail(w, r, err)
		return "", false
	}
	// Once started, finishing isn't cut short by the client going away.
	ctx := context.WithoutCancel(r.Context())
	trashID, target := "", ""
	var old *store.TrashItem
	if up.Overwrite {
		trashID = security.RandomToken(12)
		target, _ = files.Join(up.Dir, up.Name)
		uid := int64(0)
		if u != nil {
			uid = u.ID
		}
		old = &store.TrashItem{ID: trashID, DriveID: m.info.ID, UserID: uid, OrigPath: target}
		// List the file being replaced in the trash first, so it can never
		// end up there without an entry.
		if e, err := m.d.Stat(ctx, target); err == nil && !e.IsDir {
			old.Size = e.Size
			if err := s.store.AddTrash(ctx, old); err != nil {
				s.fail(w, r, err)
				return "", false
			}
		} else {
			old = nil
		}
	}
	final, replaced, err := m.d.FinishUpload(ctx, uploadState(up), trashID)
	switch {
	case replaced && old == nil:
		// The file appeared after we looked.
		uid := int64(0)
		if u != nil {
			uid = u.ID
		}
		if aerr := s.store.AddTrash(ctx, &store.TrashItem{ID: trashID, DriveID: m.info.ID, UserID: uid, OrigPath: target}); aerr != nil {
			s.log.Error("replaced file is in the trash but not listed", "drive", m.info.Name, "path", target, "trash", trashID, "err", aerr)
		}
	case !replaced && old != nil:
		_ = s.store.DeleteTrash(ctx, trashID)
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	_ = s.store.DeleteUpload(ctx, up.ID)
	go s.queueMedia(m, final)
	// Re-index every folder between the upload's target and the new file.
	for d := files.Parent(final); ; d = files.Parent(d) {
		s.reindex(m, d)
		if d == up.Dir || d == "." || !strings.HasPrefix(d, up.Dir) {
			break
		}
	}
	return final, true
}

func userOwns(u *store.User) func(*store.Upload) bool {
	return func(up *store.Upload) bool { return up.UserID == u.ID && up.ShareID == "" }
}

func (s *Server) handleUploadCreate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req uploadReq
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok || !requireWrite(w, a) {
		return
	}
	if req.Overwrite && !a.CanDelete {
		forbidden(w, "you are not allowed to replace files here")
		return
	}
	dir, err := a.resolve(req.Dir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Overwrite {
		if target, err := files.Join(dir, req.Name); err == nil && !s.checkUnlocked(w, r, u, a.DriveID, target) {
			return
		}
	}
	if req.SkipExisting && !req.Overwrite {
		if target, err := files.Join(dir, req.Name); err == nil && a.d().Exists(r.Context(), target) {
			writeJSON(w, http.StatusOK, map[string]bool{"skipped": true})
			return
		}
	}
	s.startUpload(w, r, a.m, &store.Upload{UserID: u.ID, Dir: dir, Name: req.Name, Size: req.Size, Overwrite: req.Overwrite}, 0)
}

// userUpload re-checks that the uploader can still write to the target.
func (s *Server) userUpload(w http.ResponseWriter, r *http.Request, u *store.User) (*store.Upload, *mounted, bool) {
	up, m, ok := s.loadUpload(w, r, userOwns(u))
	if !ok {
		return nil, nil, false
	}
	ps, _ := s.placesFor(r.Context(), u)
	for _, a := range ps {
		if a.DriveID == up.DriveID && a.CanWrite && (!up.Overwrite || a.CanDelete) && files.Within(a.Root, up.Dir) {
			return up, m, true
		}
	}
	forbidden(w, "you can no longer write to this folder")
	return nil, nil, false
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request, u *store.User) {
	if up, m, ok := s.userUpload(w, r, u); ok {
		s.uploadStatus(w, r, up, m)
	}
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request, u *store.User) {
	if up, m, ok := s.userUpload(w, r, u); ok {
		s.uploadChunk(w, r, up, m)
	}
}

func (s *Server) handleUploadComplete(w http.ResponseWriter, r *http.Request, u *store.User) {
	up, m, ok := s.userUpload(w, r, u)
	if !ok {
		return
	}
	// Someone may have locked the file being replaced since the upload began.
	if target, err := files.Join(up.Dir, up.Name); up.Overwrite && err == nil && !s.checkUnlocked(w, r, u, up.DriveID, target) {
		return
	}
	final, ok := s.finishUpload(w, r, up, m, u)
	if !ok {
		return
	}
	s.audit(r, u, "", "upload", m.info.ID+":"+final, fmt.Sprintf("%d bytes", up.Size))
	e, _ := m.d.Stat(r.Context(), final)
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleUploadAbort(w http.ResponseWriter, r *http.Request, u *store.User) {
	if up, m, ok := s.loadUpload(w, r, userOwns(u)); ok {
		_ = m.d.AbortUpload(r.Context(), uploadState(up))
		_ = s.store.DeleteUpload(r.Context(), up.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Trash.

type trashJSON struct {
	*store.TrashItem
	Name      string `json:"name"`
	Path      string `json:"path"`
	Place     string `json:"place"`
	PlaceName string `json:"placeName"`
	Mine      bool   `json:"mine"` // deleted by the user asking
}

// visibleTrash lists trashed items the user could delete from.
func (s *Server) visibleTrash(r *http.Request, u *store.User) ([]trashJSON, error) {
	items, err := s.store.TrashItems(r.Context())
	if err != nil {
		return nil, err
	}
	ps, err := s.placesFor(r.Context(), u)
	if err != nil {
		return nil, err
	}
	out := []trashJSON{}
	for _, it := range items {
		for _, a := range ps {
			if a.DriveID == it.DriveID && a.CanDelete && files.Within(a.Root, it.OrigPath) && it.OrigPath != a.Root {
				out = append(out, trashJSON{it, path.Base(it.OrigPath), a.rel(it.OrigPath), a.ID, a.Name, it.UserID == u.ID})
				break
			}
		}
	}
	return out, nil
}

func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request, u *store.User) {
	out, err := s.visibleTrash(r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) trashItem(w http.ResponseWriter, r *http.Request, u *store.User) (*trashJSON, *mounted, bool) {
	items, err := s.visibleTrash(r, u)
	if err != nil {
		s.fail(w, r, err)
		return nil, nil, false
	}
	for i := range items {
		if items[i].ID == r.PathValue("id") {
			m, err := s.drive(items[i].DriveID)
			if err != nil {
				s.fail(w, r, err)
				return nil, nil, false
			}
			return &items[i], m, true
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "not found")
	return nil, nil, false
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request, u *store.User) {
	it, m, ok := s.trashItem(w, r, u)
	if !ok {
		return
	}
	// Restoring writes the item back, so it needs write rights and room.
	a, err := s.placeByID(r.Context(), u, it.Place)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !requireWrite(w, a) {
		return
	}
	if err := s.quotaCheck(r.Context(), m.info.ID, files.Parent(it.OrigPath), it.Size); err != nil {
		s.quotaFail(w, err)
		return
	}
	final, err := m.d.Restore(r.Context(), it.ID, it.OrigPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.DeleteTrash(r.Context(), it.ID)
	s.reindex(m, files.Parent(final))
	s.audit(r, u, "", "restore", m.info.ID+":"+final, "")
	rel := a.rel(final)
	writeJSON(w, http.StatusOK, map[string]string{"path": rel, "place": it.Place})
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request, u *store.User) {
	it, m, ok := s.trashItem(w, r, u)
	if !ok {
		return
	}
	if err := m.d.PurgeTrash(r.Context(), it.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.DeleteTrash(r.Context(), it.ID)
	s.audit(r, u, "", "trash.purge", m.info.ID+":"+it.OrigPath, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request, u *store.User) {
	items, err := s.visibleTrash(r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Say what was left: items on a drive that's offline, or that couldn't
	// be removed, stay in the trash.
	var out struct {
		Deleted int `json:"deleted"`
		Offline int `json:"offline"`
		Failed  int `json:"failed"`
	}
	for _, it := range items {
		m, err := s.drive(it.DriveID)
		if err != nil {
			out.Offline++
			continue
		}
		if err := m.d.PurgeTrash(r.Context(), it.ID); err != nil {
			if driveGone(err) {
				out.Offline++
			} else {
				out.Failed++
				s.log.Warn("could not empty a trash item", "drive", m.info.Name, "path", it.OrigPath, "err", err)
			}
			continue
		}
		if err := s.store.DeleteTrash(r.Context(), it.ID); err != nil {
			out.Failed++
			continue
		}
		out.Deleted++
	}
	detail := fmt.Sprintf("%d items", out.Deleted)
	if left := out.Offline + out.Failed; left > 0 {
		detail += fmt.Sprintf(", %d left", left)
	}
	s.audit(r, u, "", "trash.empty", "", detail)
	writeJSON(w, http.StatusOK, out)
}
