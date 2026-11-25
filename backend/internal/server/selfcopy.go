package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/oddomens/coffer/internal/backup"
	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
	"github.com/oddomens/coffer/internal/store"
)

// selfBackupOn reports whether backups into m carry a copy of Coffer's own
// database and key. By default only drives that keep them private do: a
// local drive, or a bucket Coffer encrypts.
func (s *Server) selfBackupOn(m *mounted) bool {
	switch s.cur().SelfBackup {
	case "on":
		return true
	case "off":
		return false
	}
	return m.info.Kind == "local" || encrypted(m)
}

// selfCopyExposed names a user who isn't an admin and whose home or shared
// folder contains, or lies inside, the backup's target folder ("" if none).
func (s *Server) selfCopyExposed(ctx context.Context, b *store.Backup) string {
	places, err := s.store.SharedPlacesOn(ctx, b.TargetDrive)
	if err != nil {
		return "someone (the folders couldn't be checked)"
	}
	for _, p := range places {
		if files.Within(p.Path, b.TargetPath) || files.Within(b.TargetPath, p.Path) {
			return p.Username
		}
	}
	return ""
}

// copySelf adds a consistent copy of coffer.db, and secret.key unless the
// key comes from COFFER_SECRET_KEY, to a finished backup run.
func (s *Server) copySelf(ctx context.Context, b *store.Backup, dst *mounted, snap string) {
	if snap == "" || !s.selfBackupOn(dst) {
		return
	}
	// The copy holds the key to every stored password and drive key, so it
	// isn't written where someone other than an admin can open the folder.
	if who := s.selfCopyExposed(ctx, b); who != "" {
		s.log.Warn("not copying Coffer's database into a backup: its folder is open to a user", "backup", b.Name, "user", who)
		s.notifyAdmins("backup", "Backup “"+b.Name+"” didn't copy Coffer's own database",
			"Its folder can be opened by "+who+", and the copy includes Coffer's key. Move the backup to a folder only admins can open.", "/admin/backups")
		return
	}
	// Leftovers of a copy cut short by a restart.
	if old, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, "selfcopy-*.db")); len(old) > 0 {
		for _, p := range old {
			if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > 12*time.Hour {
				_ = os.Remove(p)
			}
		}
	}
	tmp := filepath.Join(s.cfg.DataDir, "selfcopy-"+security.RandomToken(9)+".db")
	defer os.Remove(tmp)
	err := s.store.VacuumInto(ctx, tmp)
	if err == nil {
		srcs := map[string]string{"coffer.db": tmp}
		if s.cfg.SecretKey == "" {
			srcs["secret.key"] = filepath.Join(s.cfg.DataDir, "secret.key")
		}
		err = backup.WriteSelfCopy(ctx, dst.d, b.TargetPath, snap, srcs)
	}
	if err != nil {
		s.log.Warn("copying Coffer's database into a backup", "backup", b.Name, "err", err)
		s.notifyAdmins("backup", "Backup “"+b.Name+"” couldn't copy Coffer's own database", truncate(err.Error(), 300), "/admin/backups")
		return
	}
	s.log.Info("copied Coffer's database into a backup", "backup", b.Name, "snapshot", snap)
}
