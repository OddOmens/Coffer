package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
)

// Drive is a storage backend. Paths are drive-relative, cleaned with Clean
// ("." is the root), and never include InternalDir.
type Drive interface {
	Kind() string
	Stat(ctx context.Context, p string) (Entry, error)
	List(ctx context.Context, p string, hideDot bool) ([]Entry, error)
	// Open returns the content of a regular file. *os.File for local drives,
	// so downloads keep using sendfile.
	Open(ctx context.Context, p string) (io.ReadSeekCloser, Entry, error)
	Exists(ctx context.Context, p string) bool
	Mkdir(ctx context.Context, p string) error
	MkdirAll(ctx context.Context, p string) error
	// Rename moves src to dst and never overwrites.
	Rename(ctx context.Context, src, dst string) error
	// Copy recursively copies src to dst, which must not exist.
	Copy(ctx context.Context, src, dst string) error
	RemoveAll(ctx context.Context, p string) error
	// WriteFile atomically creates or replaces p with size bytes from r.
	WriteFile(ctx context.Context, p string, r io.Reader, size int64) error
	// Walk visits root and everything below it in lexical order. Returning
	// fs.SkipDir from fn for a directory skips it.
	Walk(ctx context.Context, root string, fn func(Entry) error) error
	// Usage reports free and total bytes when the backend knows them.
	Usage(ctx context.Context) (free, total int64, ok bool)

	Trash(ctx context.Context, p, id string) (isDir bool, size int64, err error)
	Restore(ctx context.Context, id, orig string) (string, error)
	PurgeTrash(ctx context.Context, id string) error

	Uploads
	Close() error
}

// Uploads is the resumable upload protocol. Backends keep their state in
// UploadState, which the caller persists between requests.
type Uploads interface {
	// ChunkSize is the size every chunk except the last must have, or 0 for any.
	ChunkSize() int64
	StartUpload(ctx context.Context, up *UploadState) error
	UploadOffset(ctx context.Context, up *UploadState) (int64, error)
	// WriteChunk appends n bytes of r at offset. It returns the new offset.
	WriteChunk(ctx context.Context, up *UploadState, offset, n int64, r io.Reader) (int64, error)
	// FinishUpload puts the file in place. With a trashID, a file already
	// there is kept in the trash under that ID; replaced reports that it
	// is, even when err says the new file couldn't be put in place.
	FinishUpload(ctx context.Context, up *UploadState, trashID string) (final string, replaced bool, err error)
	AbortUpload(ctx context.Context, up *UploadState) error
}

type UploadState struct {
	ID        string
	Size      int64
	Dir       string // target directory
	Name      string // relative name, may contain sub-directories
	Overwrite bool
	// Backend state.
	Target string       `json:"target,omitempty"`
	Remote string       `json:"remote,omitempty"`
	Parts  []UploadPart `json:"parts,omitempty"`
	// Encrypted drives: the file's first segment nonce, hex. Its key comes
	// from it, so every chunk must use the same one.
	KeySalt string `json:"keySalt,omitempty"`
	// Which drive key sealed the chunks so far (KeyCheck).
	KeyCheck string `json:"keyCheck,omitempty"`
}

type UploadPart struct {
	N    int    `json:"n"`
	ETag string `json:"etag"`
	Size int64  `json:"size"`
}

type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
	Mime    string `json:"mime,omitempty"`
	Kind    string `json:"kind"`
	Items   int64  `json:"items,omitempty"` // files below a folder, from the index
	Symlink bool   `json:"symlink,omitempty"`
}

func newEntryFromInfo(name, p string, info fs.FileInfo) Entry {
	e := Entry{Name: name, Path: p, IsDir: info.IsDir(), ModTime: info.ModTime().Unix()}
	if !e.IsDir {
		e.Size = info.Size()
		e.Mime = MimeType(name)
	}
	e.Kind = KindOf(name, e.IsDir)
	return e
}

var (
	ErrUnsupported = errors.New("not supported by this drive")
	ErrChunkSize   = errors.New("chunk has the wrong size")
)
