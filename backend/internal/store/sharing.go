package store

import (
	"context"
	"database/sql"
	"strings"
)

// Recipient is a person a link was sent to, or who verified their email to
// open it.
type Recipient struct {
	ID        string `json:"id"`
	ShareID   string `json:"-"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	InvitedAt int64  `json:"invitedAt"` // 0 = added by verifying, not invited
	LastSeen  int64  `json:"lastSeen"`
	Views     int64  `json:"views"`
	Downloads int64  `json:"downloads"`
	Revoked   bool   `json:"revoked"`
}

const recipientCols = "id, share_id, email, name, created_at, invited_at, last_seen, views, downloads, revoked"

func scanRecipient(row interface{ Scan(...any) error }) (*Recipient, error) {
	r := &Recipient{}
	err := row.Scan(&r.ID, &r.ShareID, &r.Email, &r.Name, &r.CreatedAt, &r.InvitedAt, &r.LastSeen, &r.Views, &r.Downloads, &r.Revoked)
	return r, notFound(err)
}

// UpsertRecipient adds email to a share, or returns the existing record.
// invited marks it as invited now; name fills in a missing name.
func (s *Store) UpsertRecipient(ctx context.Context, id, shareID, email, name string, invited bool) (*Recipient, error) {
	t := now()
	inv := int64(0)
	if invited {
		inv = t
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_recipients (id, share_id, email, name, created_at, invited_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(share_id, email) DO UPDATE SET
			name = CASE WHEN share_recipients.name = '' THEN excluded.name ELSE share_recipients.name END,
			invited_at = CASE WHEN excluded.invited_at > 0 THEN excluded.invited_at ELSE share_recipients.invited_at END,
			revoked = CASE WHEN excluded.invited_at > 0 THEN 0 ELSE share_recipients.revoked END`,
		id, shareID, strings.ToLower(email), name, t, inv)
	if err != nil {
		return nil, err
	}
	return s.RecipientByEmail(ctx, shareID, email)
}

func (s *Store) RecipientByEmail(ctx context.Context, shareID, email string) (*Recipient, error) {
	return scanRecipient(s.db.QueryRowContext(ctx, "SELECT "+recipientCols+" FROM share_recipients WHERE share_id = ? AND email = ?", shareID, email))
}

func (s *Store) RecipientByID(ctx context.Context, shareID, id string) (*Recipient, error) {
	return scanRecipient(s.db.QueryRowContext(ctx, "SELECT "+recipientCols+" FROM share_recipients WHERE share_id = ? AND id = ?", shareID, id))
}

func (s *Store) Recipients(ctx context.Context, shareID string) ([]*Recipient, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+recipientCols+" FROM share_recipients WHERE share_id = ? ORDER BY created_at", shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Recipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetRecipientRevoked(ctx context.Context, shareID, id string, revoked bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE share_recipients SET revoked = ? WHERE share_id = ? AND id = ?", b2i(revoked), shareID, id)
	return err
}

func (s *Store) DeleteRecipient(ctx context.Context, shareID, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM share_recipients WHERE share_id = ? AND id = ?", shareID, id)
	return err
}

// TouchRecipient records a visit (view) or a download.
func (s *Store) TouchRecipient(ctx context.Context, id string, download bool) error {
	col := "views"
	if download {
		col = "downloads"
	}
	_, err := s.db.ExecContext(ctx, "UPDATE share_recipients SET last_seen = ?, "+col+" = "+col+" + 1 WHERE id = ?", now(), id)
	return err
}

// Verification codes for email access.

type ShareCode struct {
	Hash      string
	ExpiresAt int64
	Attempts  int
	SentAt    int64
}

func (s *Store) PutShareCode(ctx context.Context, shareID, email, hash string, expires int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO share_codes (share_id, email, code_hash, expires_at, sent_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(share_id, email) DO UPDATE SET code_hash = excluded.code_hash, expires_at = excluded.expires_at,
		attempts = 0, sent_at = excluded.sent_at`, shareID, strings.ToLower(email), hash, expires, now())
	return err
}

func (s *Store) ShareCode(ctx context.Context, shareID, email string) (*ShareCode, error) {
	c := &ShareCode{}
	err := s.db.QueryRowContext(ctx, "SELECT code_hash, expires_at, attempts, sent_at FROM share_codes WHERE share_id = ? AND email = ?",
		shareID, strings.ToLower(email)).Scan(&c.Hash, &c.ExpiresAt, &c.Attempts, &c.SentAt)
	return c, notFound(err)
}

// TryShareCode uses up one try at the live code for email and returns it,
// or ErrNotFound when there is none or it has had maxTries. The try is
// counted before the code is checked, so parallel guesses can't exceed it.
func (s *Store) TryShareCode(ctx context.Context, shareID, email string, maxTries int) (*ShareCode, error) {
	c := &ShareCode{}
	err := s.db.QueryRowContext(ctx, `UPDATE share_codes SET attempts = attempts + 1
		WHERE share_id = ? AND email = ? AND attempts < ? AND expires_at >= ?
		RETURNING code_hash, expires_at, attempts, sent_at`,
		shareID, strings.ToLower(email), maxTries, now()).Scan(&c.Hash, &c.ExpiresAt, &c.Attempts, &c.SentAt)
	return c, notFound(err)
}

func (s *Store) DeleteShareCode(ctx context.Context, shareID, email string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM share_codes WHERE share_id = ? AND email = ?", shareID, strings.ToLower(email))
	return err
}

func (s *Store) PurgeShareCodes(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM share_codes WHERE expires_at < ?", now())
	return err
}

// Collections.

type Collection struct {
	ID          string            `json:"id"`
	OwnerID     int64             `json:"-"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	CreatedAt   int64             `json:"createdAt"`
	UpdatedAt   int64             `json:"updatedAt"`
	Items       []*CollectionItem `json:"items"`
}

type CollectionItem struct {
	DriveID string `json:"-"`
	Path    string `json:"-"`
	IsDir   bool   `json:"isDir"`
	AddedAt int64  `json:"addedAt"`
}

func (s *Store) CreateCollection(ctx context.Context, c *Collection) error {
	c.CreatedAt, c.UpdatedAt = now(), now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO collections (id, owner_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		c.ID, c.OwnerID, c.Name, c.Description, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) UpdateCollection(ctx context.Context, c *Collection) error {
	c.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, "UPDATE collections SET name = ?, description = ?, updated_at = ? WHERE id = ?", c.Name, c.Description, c.UpdatedAt, c.ID)
	return err
}

func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM shares WHERE collection_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) collectionItems(ctx context.Context, id string) ([]*CollectionItem, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT drive_id, path, is_dir, added_at FROM collection_items WHERE collection_id = ? ORDER BY added_at, path", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CollectionItem{}
	for rows.Next() {
		it := &CollectionItem{}
		if err := rows.Scan(&it.DriveID, &it.Path, &it.IsDir, &it.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CollectionByID(ctx context.Context, id string) (*Collection, error) {
	c := &Collection{}
	err := s.db.QueryRowContext(ctx, "SELECT id, owner_id, name, description, created_at, updated_at FROM collections WHERE id = ?", id).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	c.Items, err = s.collectionItems(ctx, id)
	return c, err
}

func (s *Store) Collections(ctx context.Context, ownerID int64) ([]*Collection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM collections WHERE owner_id = ? ORDER BY updated_at DESC", ownerID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []*Collection{}
	for _, id := range ids {
		c, err := s.CollectionByID(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) AddCollectionItem(ctx context.Context, id string, it *CollectionItem) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO collection_items (collection_id, drive_id, path, is_dir, added_at) VALUES (?, ?, ?, ?, ?)",
		id, it.DriveID, it.Path, b2i(it.IsDir), now())
	if err == nil {
		_, err = s.db.ExecContext(ctx, "UPDATE collections SET updated_at = ? WHERE id = ?", now(), id)
	}
	return err
}

func (s *Store) RemoveCollectionItem(ctx context.Context, id, drive, p string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM collection_items WHERE collection_id = ? AND drive_id = ? AND path = ?", id, drive, p)
	return err
}

// Comments and review decisions.

const (
	CommentNote     = "comment"
	CommentApproved = "approved"
	CommentChanges  = "changes"
)

type Comment struct {
	ID        int64    `json:"id"`
	DriveID   string   `json:"-"`
	Path      string   `json:"-"`
	ShareID   string   `json:"-"`
	ParentID  int64    `json:"parentId"`
	UserID    int64    `json:"-"`
	Author    string   `json:"author"`
	Email     string   `json:"email,omitempty"`
	Kind      string   `json:"kind"` // comment | approved | changes
	Body      string   `json:"body"`
	Timecode  *float64 `json:"timecode"`
	X         *float64 `json:"x"`
	Y         *float64 `json:"y"`
	Resolved  bool     `json:"resolved"`
	Internal  bool     `json:"internal"` // team only, hidden from link visitors
	CreatedAt int64    `json:"createdAt"`
	Guest     bool     `json:"guest"` // written through a share link
}

const commentCols = "id, drive_id, path, share_id, parent_id, user_id, author, email, kind, body, timecode, x, y, resolved, internal, created_at"

func scanComment(row interface{ Scan(...any) error }) (*Comment, error) {
	c := &Comment{}
	var tc, x, y sql.NullFloat64
	err := row.Scan(&c.ID, &c.DriveID, &c.Path, &c.ShareID, &c.ParentID, &c.UserID, &c.Author, &c.Email, &c.Kind, &c.Body,
		&tc, &x, &y, &c.Resolved, &c.Internal, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if tc.Valid {
		c.Timecode = &tc.Float64
	}
	if x.Valid && y.Valid {
		c.X, c.Y = &x.Float64, &y.Float64
	}
	c.Guest = c.UserID == 0
	return c, nil
}

func nullable(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func (s *Store) AddComment(ctx context.Context, c *Comment) error {
	c.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO comments (drive_id, path, share_id, parent_id, user_id, author, email, kind, body,
		timecode, x, y, internal, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.DriveID, c.Path, c.ShareID, c.ParentID, c.UserID, c.Author, c.Email, c.Kind, c.Body,
		nullable(c.Timecode), nullable(c.X), nullable(c.Y), b2i(c.Internal), c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.Guest = c.UserID == 0
	return nil
}

func (s *Store) Comments(ctx context.Context, drive, p string) ([]*Comment, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+commentCols+" FROM comments WHERE drive_id = ? AND path = ? ORDER BY id", drive, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Comment{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CommentByID(ctx context.Context, id int64) (*Comment, error) {
	return scanComment(s.db.QueryRowContext(ctx, "SELECT "+commentCols+" FROM comments WHERE id = ?", id))
}

func (s *Store) ResolveComment(ctx context.Context, id int64, resolved bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE comments SET resolved = ? WHERE id = ?", b2i(resolved), id)
	return err
}

func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM comments WHERE id = ? OR parent_id = ?", id, id)
	return err
}

// ReviewSummary is the comment count and latest decision of one file.
type ReviewSummary struct {
	Comments int    `json:"comments"`
	Open     int    `json:"open"`
	Status   string `json:"status,omitempty"` // approved | changes
	By       string `json:"by,omitempty"`
	At       int64  `json:"at,omitempty"`
}

// ReviewsIn summarises comments on the direct children of dir.
func (s *Store) ReviewsIn(ctx context.Context, drive, dir string) (map[string]*ReviewSummary, error) {
	return s.reviewsIn(ctx, drive, dir, nil)
}

// PublicReviewsIn is ReviewsIn for visitors of the link shareID: only
// threads started through that link or by the team count, and internal
// notes and the replies under them don't.
func (s *Store) PublicReviewsIn(ctx context.Context, drive, dir, shareID string) (map[string]*ReviewSummary, error) {
	return s.reviewsIn(ctx, drive, dir, &shareID)
}

// linkThread matches comments a link's visitors may see as a thread start:
// made through that link, or by the team outside any link.
const linkThread = "(share_id = ? OR (user_id <> 0 AND share_id = ''))"

func (s *Store) reviewsIn(ctx context.Context, drive, dir string, shareID *string) (map[string]*ReviewSummary, error) {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	q := `SELECT path, kind, resolved, author, created_at, parent_id FROM comments
		WHERE drive_id = ? AND path LIKE ? ESCAPE '\'`
	args := []any{drive, likeEscape(prefix) + "%"}
	if shareID != nil {
		q += " AND internal = 0 AND ((parent_id = 0 AND " + linkThread + ") OR parent_id IN (SELECT id FROM comments WHERE internal = 0 AND " + linkThread + "))"
		args = append(args, *shareID, *shareID)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ReviewSummary{}
	for rows.Next() {
		var p, kind, author string
		var resolved bool
		var at, parent int64
		if err := rows.Scan(&p, &kind, &resolved, &author, &at, &parent); err != nil {
			return nil, err
		}
		if strings.Contains(p[len(prefix):], "/") {
			continue
		}
		r := out[p]
		if r == nil {
			r = &ReviewSummary{}
			out[p] = r
		}
		switch kind {
		case CommentApproved, CommentChanges:
			r.Status, r.By, r.At = kind, author, at
		default:
			r.Comments++
			if !resolved && parent == 0 {
				r.Open++
			}
		}
	}
	return out, rows.Err()
}

// CommentAuthors lists the users (not guests) who commented on a file.
func (s *Store) CommentAuthors(ctx context.Context, drive, p string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT user_id FROM comments WHERE drive_id = ? AND path = ? AND user_id != 0", drive, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
