package store

import "context"

// Drive is a configured storage backend.
type Drive struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // local | s3
	Config    string `json:"-"`    // JSON; secrets are sealed
	Sort      int    `json:"sort"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) Drives(ctx context.Context) ([]*Drive, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, kind, config, sort, disabled, created_at FROM drives ORDER BY sort, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Drive
	for rows.Next() {
		d := &Drive{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.Config, &d.Sort, &d.Disabled, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateDrive(ctx context.Context, d *Drive) error {
	d.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, "INSERT INTO drives (id, name, kind, config, sort, disabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		d.ID, d.Name, d.Kind, d.Config, d.Sort, b2i(d.Disabled), d.CreatedAt)
	return err
}

func (s *Store) UpdateDrive(ctx context.Context, d *Drive) error {
	_, err := s.db.ExecContext(ctx, "UPDATE drives SET name = ?, config = ?, sort = ?, disabled = ? WHERE id = ?",
		d.Name, d.Config, d.Sort, b2i(d.Disabled), d.ID)
	return err
}

// DeleteDrive removes a drive with its places, shares, trash records and uploads.
func (s *Store) DeleteDrive(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM shares WHERE drive_id = ?", "DELETE FROM trash WHERE drive_id = ?",
		"DELETE FROM uploads WHERE drive_id = ?", "DELETE FROM places WHERE drive_id = ?",
		"DELETE FROM collection_items WHERE drive_id = ?", "DELETE FROM comments WHERE drive_id = ?",
		"DELETE FROM favorites WHERE drive_id = ?", "DELETE FROM tags WHERE drive_id = ?", "DELETE FROM locks WHERE drive_id = ?",
		"DELETE FROM media_jobs WHERE drive_id = ?",
		"DELETE FROM backups WHERE source_drive = ?1 OR target_drive = ?1", "DELETE FROM drives WHERE id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Place gives a user access to one folder of one drive.
type Place struct {
	ID          string `json:"id"`
	UserID      int64  `json:"userId"`
	DriveID     string `json:"driveId"`
	Path        string `json:"-"` // drive-relative root
	Name        string `json:"name"`
	CanWrite    bool   `json:"canWrite"`
	CanDelete   bool   `json:"canDelete"`
	CanShare    bool   `json:"canShare"`
	GrantedBy   int64  `json:"grantedById"`
	GrantedName string `json:"grantedBy"`
	Username    string `json:"username"`
	CreatedAt   int64  `json:"createdAt"`
}

const placeCols = `p.id, p.user_id, p.drive_id, p.path, p.name, p.can_write, p.can_delete, p.can_share, p.granted_by,
	COALESCE(g.username, ''), u.username, p.created_at`
const placeFrom = ` FROM places p JOIN users u ON u.id = p.user_id LEFT JOIN users g ON g.id = p.granted_by`

func scanPlaces(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]*Place, error) {
	defer rows.Close()
	out := []*Place{}
	for rows.Next() {
		p := &Place{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.DriveID, &p.Path, &p.Name, &p.CanWrite, &p.CanDelete, &p.CanShare,
			&p.GrantedBy, &p.GrantedName, &p.Username, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PlacesForUser(ctx context.Context, userID int64) ([]*Place, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+placeCols+placeFrom+" WHERE p.user_id = ? ORDER BY p.granted_by != 0, p.name", userID)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}

func (s *Store) PlacesGrantedBy(ctx context.Context, userID int64) ([]*Place, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+placeCols+placeFrom+" WHERE p.granted_by = ? ORDER BY p.created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}

func (s *Store) PlaceByID(ctx context.Context, id string) (*Place, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+placeCols+placeFrom+" WHERE p.id = ?", id)
	if err != nil {
		return nil, err
	}
	ps, err := scanPlaces(rows)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ErrNotFound
	}
	return ps[0], nil
}

func (s *Store) CreatePlace(ctx context.Context, p *Place) error {
	p.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO places (id, user_id, drive_id, path, name, can_write, can_delete, can_share,
		granted_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.ID, p.UserID, p.DriveID, p.Path, p.Name,
		b2i(p.CanWrite), b2i(p.CanDelete), b2i(p.CanShare), p.GrantedBy, p.CreatedAt)
	return err
}

func (s *Store) UpdatePlace(ctx context.Context, p *Place) error {
	_, err := s.db.ExecContext(ctx, "UPDATE places SET name = ?, path = ?, can_write = ?, can_delete = ?, can_share = ? WHERE id = ?",
		p.Name, p.Path, b2i(p.CanWrite), b2i(p.CanDelete), b2i(p.CanShare), p.ID)
	return err
}

func (s *Store) DeletePlace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM places WHERE id = ?", id)
	return err
}

// SharedPlacesOn lists the folders on a drive that people who aren't admins
// can open: their home folders and folders shared with them.
func (s *Store) SharedPlacesOn(ctx context.Context, drive string) ([]*Place, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+placeCols+placeFrom+" WHERE p.drive_id = ? AND u.role != 'admin'", drive)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}

// HomePlacesOn lists the home folders (not grants) on a drive, with their
// owners' storage limits.
func (s *Store) HomePlacesOn(ctx context.Context, drive string) ([]*Place, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+placeCols+placeFrom+" WHERE p.drive_id = ? AND p.granted_by = 0", drive)
	if err != nil {
		return nil, err
	}
	return scanPlaces(rows)
}
