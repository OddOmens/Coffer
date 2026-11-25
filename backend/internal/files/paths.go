// Package files performs every filesystem operation through an os.Root, so
// no path, symlink or race can reach outside the storage directory.
package files

import (
	"errors"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InternalDir holds Coffer's upload staging and trash inside the storage root,
// so moves into and out of it are atomic renames. It is never listed or served.
const InternalDir = ".coffer"

// SelfCopyDir is the folder inside a backup's target that holds copies of
// Coffer's own database and key. It counts as hidden, so share links never
// serve it, and only admins browsing a whole drive can open it.
const SelfCopyDir = "_coffer"

var (
	ErrInvalidPath = errors.New("invalid path")
	ErrInvalidName = errors.New("invalid name")
	ErrExists      = errors.New("already exists")
	ErrNotDir      = errors.New("not a directory")
	ErrIsDir       = errors.New("is a directory")
	ErrIntoSelf    = errors.New("cannot move a folder into itself")
)

// Clean turns a user-supplied path into a storage-relative path ("." for the
// root). ".." can never climb above the root, and the internal dir is refused.
func Clean(p string) (string, error) {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return "", ErrInvalidPath
	}
	c := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	if c == "" {
		return ".", nil
	}
	if first, _, _ := strings.Cut(c, "/"); isInternal(first) {
		return "", ErrInvalidPath
	}
	return c, nil
}

// isInternal matches InternalDir in any case: on a case-insensitive volume
// ".COFFER" is the same folder.
func isInternal(name string) bool { return strings.EqualFold(name, InternalDir) }

// Resolver is implemented by drives that can hold symlinks.
type Resolver interface {
	// RealPath resolves the symlinks in p. The result stays inside the drive.
	RealPath(p string) (string, error)
}

// Confine is Join that also follows p's symlinks on drives that have them:
// the real path must stay inside scope (and out of InternalDir), so a link
// can't lead from one user's folder into another part of the drive.
func Confine(d Drive, scope, p string) (string, error) {
	jp, err := Join(scope, p)
	if err != nil {
		return "", err
	}
	r, ok := d.(Resolver)
	if !ok {
		return jp, nil
	}
	real, err := r.RealPath(jp)
	if err != nil {
		return "", err
	}
	if _, err := Clean(real); err != nil {
		return "", err
	}
	if scope != "." && scope != "" {
		rs, err := r.RealPath(scope)
		if err != nil {
			return "", err
		}
		if !Within(rs, real) {
			return "", ErrInvalidPath
		}
	}
	return jp, nil
}

// Join resolves p (relative to a user's scope) into a storage-relative path
// that is guaranteed to stay inside scope.
func Join(scope, p string) (string, error) {
	rel, err := Clean(p)
	if err != nil {
		return "", err
	}
	if scope == "." || scope == "" {
		return rel, nil
	}
	if rel == "." {
		return scope, nil
	}
	return scope + "/" + rel, nil
}

// Within reports whether storage-relative path p lies inside scope.
func Within(scope, p string) bool {
	return scope == "." || p == scope || strings.HasPrefix(p, scope+"/")
}

// Rel converts storage-relative p into the "/a/b" form shown to a user whose
// scope is scope.
func Rel(scope, p string) string {
	if scope != "." {
		p = strings.TrimPrefix(strings.TrimPrefix(p, scope), "/")
	}
	if p == "." || p == "" {
		return "/"
	}
	return "/" + p
}

// Parent returns the parent of a storage-relative path.
func Parent(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

// ValidName checks a single path segment for creation. It allows anything
// macOS, Windows (via SMB) and Linux all handle sanely.
func ValidName(n string) error {
	if n == "" || n == "." || n == ".." || len(n) > 255 || !utf8.ValidString(n) {
		return ErrInvalidName
	}
	if strings.ContainsAny(n, "/\\\x00") || n != strings.TrimSpace(n) || strings.HasSuffix(n, ".") {
		return ErrInvalidName
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return ErrInvalidName
		}
	}
	if isInternal(n) {
		return ErrInvalidName
	}
	return nil
}

// FixName turns n into a name ValidName allows, as close to n as it can:
// control characters are dropped, backslashes and broken UTF-8 become "_",
// spaces around it and dots at its end are trimmed, and a long name is cut
// to 255 bytes, keeping its extension. It returns "" for the names Coffer
// keeps for itself.
func FixName(n string) string {
	n = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return -1
		case r == '\\' || r == '/':
			return '_'
		}
		return r
	}, strings.ToValidUTF8(n, "_"))
	trim := func(s string) string {
		return strings.TrimRightFunc(strings.TrimSpace(s), func(r rune) bool { return r == '.' || unicode.IsSpace(r) })
	}
	n = trim(n)
	if len(n) > 255 {
		ext := path.Ext(n)
		if len(ext) > 32 {
			ext = ""
		}
		base := n[:255-len(ext)]
		for !utf8.ValidString(base) {
			base = base[:len(base)-1]
		}
		n = trim(base) + ext
	}
	if n == "" || n == "." || n == ".." {
		n = "_"
	}
	if ValidName(n) != nil {
		return ""
	}
	return n
}

// ValidEntryName is the looser check for naming an existing entry, which may
// have been created over SMB with characters ValidName refuses.
func ValidEntryName(n string) bool {
	return n != "" && n != "." && n != ".." && !isInternal(n) && !strings.ContainsAny(n, "/\x00")
}

// ValidRelName checks a relative name that may include sub-directories
// ("folder/sub/file.ext"), as sent by folder uploads.
func ValidRelName(n string) error {
	parts := strings.Split(n, "/")
	if len(parts) > 32 {
		return ErrInvalidName
	}
	for _, p := range parts {
		if err := ValidName(p); err != nil {
			return err
		}
	}
	return nil
}

func join(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// Hidden reports whether a directory entry is hidden from listings.
func Hidden(name string, hideDot bool) bool {
	if isInternal(name) {
		return true
	}
	if !hideDot {
		return false
	}
	return strings.HasPrefix(name, ".") || strings.EqualFold(name, SelfCopyDir) || name == "Thumbs.db" || name == "desktop.ini" || name == "$RECYCLE.BIN"
}
