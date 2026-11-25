package server

import (
	"context"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/index"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// access is one root a user can browse: a whole drive (admins), a home
// folder, or a folder someone shared with them.
type access struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DriveID   string `json:"driveId"`
	DriveName string `json:"driveName"`
	DriveKind string `json:"driveKind"`
	Encrypted bool   `json:"encrypted,omitempty"`
	Kind      string `json:"kind"` // drive | home | shared
	CanWrite  bool   `json:"canWrite"`
	CanDelete bool   `json:"canDelete"`
	CanShare  bool   `json:"canShare"`
	GrantedBy string `json:"grantedBy,omitempty"`
	Online    bool   `json:"online"`
	Root      string `json:"-"`
	RootPath  string `json:"root"` // the root on the drive, as /a/b
	m         *mounted
}

func (a *access) rel(p string) string { return files.Rel(a.Root, p) }
func (a *access) d() files.Drive      { return a.m.d }

// resolve maps a place-relative path to a drive path. Symlinks must not lead
// out of the place.
func (a *access) resolve(p string) (string, error) {
	// Copies of Coffer's database and key inside a backup are for admins
	// browsing the drive itself, never for a home or shared folder.
	// The check runs on the cleaned path ("a\\_coffer" is "a/_coffer") and
	// on the drive path it resolves to, in any case.
	if a.Kind != "drive" {
		c, err := files.Clean(p)
		if err != nil {
			return "", err
		}
		if hasSelfCopy(c) {
			return "", files.ErrInvalidPath
		}
	}
	var out string
	var err error
	if a.m == nil || a.m.d == nil {
		out, err = files.Join(a.Root, p)
	} else {
		out, err = files.Confine(a.m.d, a.Root, p)
	}
	if err == nil && a.Kind != "drive" && hasSelfCopy(out) {
		return "", files.ErrInvalidPath
	}
	return out, err
}

// hasSelfCopy reports whether a cleaned path goes through a folder holding
// copies of Coffer's database and key.
func hasSelfCopy(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.EqualFold(seg, files.SelfCopyDir) {
			return true
		}
	}
	return false
}

// visible drops listed symlinks that lead out of the place.
func (a *access) visible(es []files.Entry) []files.Entry {
	out := es[:0]
	for _, e := range es {
		if e.Symlink {
			if _, err := a.resolve(a.rel(e.Path)); err != nil {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

const drivePlacePrefix = "d-"

// placesFor lists everything u may browse, dropping grants whose granter
// has since lost access.
func (s *Server) placesFor(ctx context.Context, u *store.User) ([]*access, error) {
	var out []*access
	if u.IsAdmin() {
		for _, m := range s.driveList() {
			out = append(out, &access{
				ID: drivePlacePrefix + m.info.ID, Name: m.info.Name, DriveID: m.info.ID, DriveName: m.info.Name,
				DriveKind: m.info.Kind, Encrypted: encrypted(m), Kind: "drive", CanWrite: true, CanDelete: true, CanShare: true,
				Online: m.d != nil, Root: ".", RootPath: "/", m: m,
			})
		}
	}
	ps, err := s.store.PlacesForUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		m, _ := s.drive(p.DriveID)
		if m == nil {
			continue
		}
		kind := "home"
		if p.GrantedBy != 0 {
			if !s.grantValid(ctx, p, need{}, 3) {
				continue
			}
			// A grant never passes on more than the granter still has.
			p.CanWrite = p.CanWrite && s.grantValid(ctx, p, need{write: true}, 3)
			p.CanDelete = p.CanDelete && s.grantValid(ctx, p, need{del: true}, 3)
			kind = "shared"
		}
		out = append(out, &access{
			ID: p.ID, Name: p.Name, DriveID: p.DriveID, DriveName: m.info.Name, DriveKind: m.info.Kind, Encrypted: encrypted(m), Kind: kind,
			CanWrite: p.CanWrite, CanDelete: p.CanDelete, CanShare: p.CanShare, GrantedBy: p.GrantedName,
			Online: m.d != nil, Root: p.Path, RootPath: files.Rel(".", p.Path), m: m,
		})
	}
	return out, nil
}

// need is the rights asked of a place.
type need struct{ share, write, del bool }

// grantValid checks that the user who shared p can still share it, with
// the write or delete rights asked of the grant.
func (s *Server) grantValid(ctx context.Context, p *store.Place, n need, depth int) bool {
	granter, err := s.store.UserByID(ctx, p.GrantedBy)
	if err != nil || granter.Disabled {
		return false
	}
	n.share = true
	return s.reaches(ctx, granter, p.DriveID, p.Path, n, depth-1)
}

// canReach reports whether u can access drive:path, with share and/or write
// rights when asked, following grants up to depth levels.
func (s *Server) canReach(ctx context.Context, u *store.User, drive, p string, share, write bool, depth int) bool {
	return s.reaches(ctx, u, drive, p, need{share: share, write: write}, depth)
}

func (s *Server) reaches(ctx context.Context, u *store.User, drive, p string, n need, depth int) bool {
	if u.Disabled || depth < 0 {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	ps, err := s.store.PlacesForUser(ctx, u.ID)
	if err != nil {
		return false
	}
	for _, pl := range ps {
		if pl.DriveID != drive || !files.Within(pl.Path, p) || (n.share && !pl.CanShare) || (n.write && !pl.CanWrite) ||
			(n.del && !pl.CanDelete) {
			continue
		}
		// Rights passed on by a grant hold only while the granter has them.
		if pl.GrantedBy == 0 || s.grantValid(ctx, pl, need{write: n.write, del: n.del}, depth) {
			return true
		}
	}
	return false
}

var errNoPlace = errors.New("no such place")

func (s *Server) placeByID(ctx context.Context, u *store.User, id string) (*access, error) {
	ps, err := s.placesFor(ctx, u)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.ID == id {
			if !p.Online {
				return nil, errDriveOffline
			}
			return p, nil
		}
	}
	return nil, errNoPlace
}

// place resolves the "place" query/body parameter of a request.
func (s *Server) place(w http.ResponseWriter, r *http.Request, u *store.User, id string) (*access, bool) {
	if id == "" {
		id = r.URL.Query().Get("place")
	}
	a, err := s.placeByID(r.Context(), u, id)
	if err != nil {
		if errors.Is(err, errDriveOffline) {
			writeErr(w, http.StatusServiceUnavailable, "drive_offline", "this drive is offline")
		} else {
			writeErr(w, http.StatusNotFound, "no_place", "folder not found")
		}
		return nil, false
	}
	return a, true
}

// homePlace is where a user lands: their own home folder, else the
// default drive (admins), else the first place.
func homePlace(ps []*access) *access {
	for _, p := range ps {
		if p.Kind == "home" && p.Online {
			return p
		}
	}
	for _, p := range ps {
		if p.Kind == "drive" && p.DriveID == "files" && p.Online {
			return p
		}
	}
	for _, p := range ps {
		if p.Kind != "shared" && p.Online {
			return p
		}
	}
	if len(ps) > 0 {
		return ps[0]
	}
	return nil
}

type placeOut struct {
	*access
	Size  int64 `json:"size"`
	Items int64 `json:"items"`
	Free  int64 `json:"free"`
	Total int64 `json:"total"`
}

func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request, u *store.User) {
	ps, err := s.placesFor(r.Context(), u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []placeOut{}
	for _, p := range ps {
		po := placeOut{access: p}
		if fsz, ok := s.index.Size(r.Context(), p.DriveID, p.Root); ok {
			po.Size, po.Items = fsz.Size, fsz.Items
		}
		if p.Online && p.Kind == "drive" {
			if free, total, ok := p.d().Usage(r.Context()); ok {
				po.Free, po.Total = free, total
			}
		}
		out = append(out, po)
	}
	home := ""
	if h := homePlace(ps); h != nil {
		home = h.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out, "home": home})
}

func scopes(ps []*access) []index.Scope {
	var out []index.Scope
	for _, p := range ps {
		if p.Online {
			out = append(out, index.Scope{Drive: p.DriveID, Root: p.Root, Tag: p.ID})
		}
	}
	return out
}

// placeOf maps a search hit back to the place-relative path.
func placeOf(ps []*access, h index.Hit) (*access, bool) {
	for _, p := range ps {
		if p.ID == h.Tag {
			return p, true
		}
	}
	return nil, false
}

// Internal sharing: give other users a folder of yours.

func (s *Server) handleUserDirectory(w http.ResponseWriter, r *http.Request, u *store.User) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type entry struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	out := []entry{}
	for _, x := range users {
		if !x.Disabled && x.ID != u.ID {
			out = append(out, entry{x.ID, x.Username})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGrant(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		Place     string  `json:"place"`
		Path      string  `json:"path"`
		Users     []int64 `json:"users"`
		CanWrite  bool    `json:"canWrite"`
		CanDelete bool    `json:"canDelete"`
		CanShare  bool    `json:"canShare"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	a, ok := s.place(w, r, u, req.Place)
	if !ok {
		return
	}
	if !a.CanShare {
		forbidden(w, "you cannot share from this folder")
		return
	}
	p, err := a.resolve(req.Path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := a.d().Stat(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !e.IsDir {
		badRequest(w, "only folders can be shared with people; use a link for single files")
		return
	}
	if len(req.Users) == 0 || len(req.Users) > 100 {
		badRequest(w, "pick who to share with")
		return
	}
	name := path.Base(p)
	if p == "." {
		name = a.DriveName
	}
	added := 0
	for _, uid := range req.Users {
		target, err := s.store.UserByID(r.Context(), uid)
		if err != nil || target.Disabled || target.ID == u.ID {
			continue
		}
		pl := &store.Place{
			ID: security.RandomToken(9), UserID: target.ID, DriveID: a.DriveID, Path: p, Name: name,
			CanWrite: req.CanWrite && a.CanWrite, CanDelete: req.CanDelete && a.CanDelete, CanShare: req.CanShare,
			GrantedBy: u.ID,
		}
		if err := s.store.CreatePlace(r.Context(), pl); err != nil {
			s.fail(w, r, err)
			return
		}
		added++
		s.notify(target.ID, "shared", u.Username+" shared “"+name+"” with you", "", "/files/"+pl.ID)
		s.audit(r, u, "", "grant.create", a.DriveID+":"+p, "to "+target.Username)
	}
	writeJSON(w, http.StatusCreated, map[string]int{"added": added})
}

func (s *Server) handleGrantsList(w http.ResponseWriter, r *http.Request, u *store.User) {
	ps, err := s.store.PlacesGrantedBy(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type out struct {
		*store.Place
		Path string `json:"path"`
	}
	res := []out{}
	for _, p := range ps {
		res = append(res, out{p, "/" + strings.TrimPrefix(p.Path, ".")})
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGrantUpdate(w http.ResponseWriter, r *http.Request, u *store.User) {
	var req struct {
		CanWrite  *bool `json:"canWrite"`
		CanDelete *bool `json:"canDelete"`
		CanShare  *bool `json:"canShare"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := s.store.PlaceByID(r.Context(), r.PathValue("id"))
	if err != nil || (p.GrantedBy != u.ID && !u.IsAdmin()) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	// Like a new grant, never more than the person changing it has there.
	ctx := r.Context()
	if req.CanWrite != nil {
		p.CanWrite = *req.CanWrite && s.reaches(ctx, u, p.DriveID, p.Path, need{write: true}, 3)
	}
	if req.CanDelete != nil {
		p.CanDelete = *req.CanDelete && s.reaches(ctx, u, p.DriveID, p.Path, need{del: true}, 3)
	}
	if req.CanShare != nil {
		p.CanShare = *req.CanShare
	}
	if err := s.store.UpdatePlace(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handlePlaceDelete removes a grant: the granter, the recipient (leaving) or an admin.
func (s *Server) handlePlaceDelete(w http.ResponseWriter, r *http.Request, u *store.User) {
	p, err := s.store.PlaceByID(r.Context(), r.PathValue("id"))
	if err != nil || !(p.GrantedBy == u.ID || (p.UserID == u.ID && p.GrantedBy != 0) || u.IsAdmin()) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := s.store.DeletePlace(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, u, "", "grant.delete", p.DriveID+":"+p.Path, "for "+p.Username)
	w.WriteHeader(http.StatusNoContent)
}

// Admin: a user's home folders.

func (s *Server) handleUserPlaces(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id := parseID(r.PathValue("id"))
	ps, err := s.store.PlacesForUser(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	type out struct {
		*store.Place
		Path      string `json:"path"`
		DriveName string `json:"driveName"`
	}
	res := []out{}
	for _, p := range ps {
		dn := p.DriveID
		if m, _ := s.drive(p.DriveID); m != nil {
			dn = m.info.Name
		}
		res = append(res, out{p, files.Rel(".", p.Path), dn})
	}
	writeJSON(w, http.StatusOK, res)
}

type placeReq struct {
	DriveID   string `json:"driveId"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	CanWrite  bool   `json:"canWrite"`
	CanDelete bool   `json:"canDelete"`
	CanShare  bool   `json:"canShare"`
	Create    bool   `json:"create"`
}

func (s *Server) validPlace(w http.ResponseWriter, r *http.Request, req placeReq) (string, *mounted, bool) {
	m, err := s.drive(req.DriveID)
	if err != nil {
		badRequest(w, "pick an online drive")
		return "", nil, false
	}
	p, err := files.Clean(req.Path)
	if err != nil {
		s.fail(w, r, err)
		return "", nil, false
	}
	if req.Create {
		if err := m.d.MkdirAll(r.Context(), p); err != nil {
			s.fail(w, r, err)
			return "", nil, false
		}
	} else if e, err := m.d.Stat(r.Context(), p); err != nil || !e.IsDir {
		badRequest(w, "that folder does not exist on the drive")
		return "", nil, false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		badRequest(w, "give the folder a display name (up to 64 characters)")
		return "", nil, false
	}
	return p, m, true
}

func (s *Server) handleUserPlaceCreate(w http.ResponseWriter, r *http.Request, admin *store.User) {
	var req placeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	target, err := s.store.UserByID(r.Context(), parseID(r.PathValue("id")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, m, ok := s.validPlace(w, r, req)
	if !ok {
		return
	}
	pl := &store.Place{ID: security.RandomToken(9), UserID: target.ID, DriveID: m.info.ID, Path: p, Name: strings.TrimSpace(req.Name),
		CanWrite: req.CanWrite, CanDelete: req.CanDelete, CanShare: req.CanShare}
	if err := s.store.CreatePlace(r.Context(), pl); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, admin, "", "place.create", m.info.ID+":"+p, "for "+target.Username)
	writeJSON(w, http.StatusCreated, pl)
}

func (s *Server) handleAdminPlaceUpdate(w http.ResponseWriter, r *http.Request, admin *store.User) {
	var req placeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	pl, err := s.store.PlaceByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	req.DriveID = pl.DriveID
	p, _, ok := s.validPlace(w, r, req)
	if !ok {
		return
	}
	pl.Path, pl.Name, pl.CanWrite, pl.CanDelete, pl.CanShare = p, strings.TrimSpace(req.Name), req.CanWrite, req.CanDelete, req.CanShare
	if err := s.store.UpdatePlace(r.Context(), pl); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.store.DeleteUserSessions(r.Context(), pl.UserID, "")
	s.audit(r, admin, "", "place.update", pl.DriveID+":"+p, "for "+pl.Username)
	writeJSON(w, http.StatusOK, pl)
}

func parseID(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}
