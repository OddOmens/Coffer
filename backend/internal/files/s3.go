package files

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config describes an S3-compatible bucket such as Cloudflare R2, AWS S3,
// Backblaze B2 or MinIO.
type S3Config struct {
	Endpoint  string `json:"endpoint"` // host[:port], e.g. <account>.r2.cloudflarestorage.com
	Region    string `json:"region"`   // "auto" for R2
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"` // optional folder inside the bucket
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Insecure  bool   `json:"insecure"`  // plain HTTP, for a LAN MinIO only
	PathStyle bool   `json:"pathStyle"` // force path-style addressing
	// DirectDownloads redirects downloads to short-lived presigned URLs so the
	// bytes come straight from the bucket's edge instead of through Coffer.
	DirectDownloads bool `json:"directDownloads"`
	// Encrypt seals file contents before they reach the bucket (crypt.go).
	Encrypt bool `json:"encrypt"`
	// Key is the drive key when Encrypt is on. It is never serialised: the
	// server keeps it sealed with the instance key.
	Key []byte `json:"-"`
}

// S3 is a drive backed by a bucket. Folders are key prefixes; empty folders
// are kept with a zero-byte "name/" marker object.
type S3 struct {
	cfg    S3Config
	core   *minio.Core
	c      *minio.Client
	prefix string
}

const s3ChunkSize = 16 << 20 // R2 needs equal-sized parts; 16 MiB matches the web client

func OpenS3(cfg S3Config) (*S3, error) {
	// Pasted keys often carry a stray space or line break.
	for _, f := range []*string{&cfg.Endpoint, &cfg.Region, &cfg.Bucket, &cfg.AccessKey, &cfg.SecretKey} {
		*f = strings.TrimSpace(*f)
	}
	cfg.Endpoint = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "https://"), "http://"), "/")
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("endpoint, bucket, access key and secret key are required")
	}
	if r := endpointRegion(cfg.Endpoint); r != "" {
		cfg.Region = r // requests are signed for the endpoint's region, whatever the form said
	} else if cfg.Region == "" {
		cfg.Region = "auto"
	}
	if cfg.Encrypt {
		if len(cfg.Key) != DriveKeySize {
			return nil, errors.New("the drive's encryption key is missing")
		}
		cfg.DirectDownloads = false // the bucket only holds ciphertext
	}
	lookup := minio.BucketLookupAuto
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	core, err := minio.NewCore(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       !cfg.Insecure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &S3{cfg: cfg, core: core, c: core.Client, prefix: prefix}, nil
}

// Check verifies the credentials can list and write the bucket.
func (s *S3) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.prefix, MaxKeys: 1}) {
		if obj.Err != nil {
			return friendlyS3(obj.Err)
		}
		break
	}
	key := s.prefix + InternalDir + "/.write-test"
	if _, err := s.c.PutObject(ctx, s.cfg.Bucket, key, bytes.NewReader(nil), 0, minio.PutObjectOptions{}); err != nil {
		return friendlyS3(err)
	}
	return friendlyS3(s.c.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}))
}

// regionEndpoint matches endpoints that name their region, such as
// s3.us-west-004.backblazeb2.com and s3.eu-west-1.amazonaws.com.
var regionEndpoint = regexp.MustCompile(`^s3[.-]([a-z0-9-]+)\.(backblazeb2|amazonaws)\.com(:\d+)?$`)

func endpointRegion(endpoint string) string {
	if m := regionEndpoint.FindStringSubmatch(strings.ToLower(endpoint)); m != nil {
		return m[1]
	}
	return ""
}

func friendlyS3(err error) error {
	if err == nil {
		return nil
	}
	r := minio.ToErrorResponse(err)
	switch r.Code {
	case "NoSuchBucket":
		return errors.New("the bucket does not exist: check its spelling, and that the endpoint is the one shown on the bucket")
	case "InvalidAccessKeyId":
		return errors.New("the access key ID is not known here: check it, and that the endpoint's region is your account's")
	case "SignatureDoesNotMatch":
		return errors.New("the secret key doesn't match the access key ID: paste it again")
	case "AuthorizationHeaderMalformed", "IllegalLocationConstraintException":
		return errors.New("the region doesn't match the endpoint: use the region from the endpoint, such as us-east-005")
	case "AccessDenied":
		return fmt.Errorf("access denied: check that the key may read and write this bucket (%s)", r.Message)
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fmt.Errorf("can't reach %s: check the endpoint's spelling", dns.Name)
	}
	return err
}

func (s *S3) Kind() string     { return "s3" }
func (s *S3) Close() error     { return nil }
func (s *S3) Config() S3Config { return s.cfg }

func (s *S3) key(p string) string {
	if p == "." || p == "" {
		return s.prefix
	}
	return s.prefix + p
}

func (s *S3) dirKey(p string) string {
	k := s.key(p)
	if k != "" && !strings.HasSuffix(k, "/") {
		k += "/"
	}
	return k
}

func (s *S3) rel(key string) string {
	r := strings.TrimSuffix(strings.TrimPrefix(key, s.prefix), "/")
	if r == "" {
		return "."
	}
	return r
}

func isNotFound(err error) bool {
	r := minio.ToErrorResponse(err)
	return r.StatusCode == http.StatusNotFound || r.Code == "NoSuchKey" || r.Code == "NotFound"
}

func (s *S3) objEntry(p string, info minio.ObjectInfo) Entry {
	name := path.Base(p)
	size := info.Size
	if s.cfg.Encrypt {
		// A size no sealed file can have is left as is; opening it fails.
		if n, ok := plainSize(size); ok {
			size = n
		}
	}
	e := Entry{Name: name, Path: p, Size: size, ModTime: info.LastModified.Unix(), Mime: MimeType(name), Kind: KindOf(name, false)}
	return e
}

func dirEntry(p string) Entry {
	return Entry{Name: path.Base(p), Path: p, IsDir: true, Kind: "folder"}
}

func (s *S3) Stat(ctx context.Context, p string) (Entry, error) {
	if p == "." {
		return dirEntry("."), nil
	}
	info, err := s.c.StatObject(ctx, s.cfg.Bucket, s.key(p), minio.StatObjectOptions{})
	if err == nil {
		return s.objEntry(p, info), nil
	}
	if !isNotFound(err) {
		return Entry{}, friendlyS3(err)
	}
	if s.dirExists(ctx, p) {
		return dirEntry(p), nil
	}
	return Entry{}, fs.ErrNotExist
}

func (s *S3) dirExists(ctx context.Context, p string) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Recursive, so only real objects count: some servers keep empty
	// "directory" prefixes around after their contents are deleted.
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.dirKey(p), Recursive: true, MaxKeys: 1}) {
		return obj.Err == nil
	}
	return false
}

func (s *S3) List(ctx context.Context, p string, hideDot bool) ([]Entry, error) {
	prefix := s.dirKey(p)
	out := []Entry{}
	found := p == "."
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix}) {
		if obj.Err != nil {
			return nil, friendlyS3(obj.Err)
		}
		found = true
		if obj.Key == prefix {
			continue // the folder's own marker
		}
		rel := s.rel(obj.Key)
		name := path.Base(rel)
		if Hidden(name, hideDot) {
			continue
		}
		if strings.HasSuffix(obj.Key, "/") {
			out = append(out, dirEntry(rel))
		} else {
			out = append(out, s.objEntry(rel, obj))
		}
	}
	if !found {
		if _, err := s.Stat(ctx, p); err == nil {
			return nil, ErrNotDir
		}
		return nil, fs.ErrNotExist
	}
	SortEntries(out)
	return out, nil
}

func (s *S3) Open(ctx context.Context, p string) (io.ReadSeekCloser, Entry, error) {
	info, err := s.c.StatObject(ctx, s.cfg.Bucket, s.key(p), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			if s.dirExists(ctx, p) {
				return nil, Entry{}, ErrIsDir
			}
			return nil, Entry{}, fs.ErrNotExist
		}
		return nil, Entry{}, friendlyS3(err)
	}
	// Not tied to the request context: ServeContent reads after headers go out.
	obj, err := s.c.GetObject(context.WithoutCancel(ctx), s.cfg.Bucket, s.key(p), minio.GetObjectOptions{})
	if err != nil {
		return nil, Entry{}, friendlyS3(err)
	}
	if s.cfg.Encrypt {
		r, err := newOpener(s.cfg.Key, obj, info.Size)
		if err != nil {
			obj.Close()
			return nil, Entry{}, err
		}
		r.head = func() ([]byte, error) { return s.firstNonce(context.WithoutCancel(ctx), s.key(p)) }
		return r, s.objEntry(p, info), nil
	}
	return obj, s.objEntry(p, info), nil
}

// firstNonce reads just the first segment's nonce of a sealed object.
func (s *S3) firstNonce(ctx context.Context, key string) ([]byte, error) {
	opts := minio.GetObjectOptions{}
	if err := opts.SetRange(0, nonceSize-1); err != nil {
		return nil, err
	}
	obj, err := s.c.GetObject(ctx, s.cfg.Bucket, key, opts)
	if err != nil {
		return nil, friendlyS3(err)
	}
	defer obj.Close()
	b := make([]byte, nonceSize)
	if _, err := io.ReadFull(obj, b); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrDamaged
		}
		return nil, friendlyS3(err)
	}
	return b, nil
}

// PresignedGet returns a short-lived URL that downloads p as an attachment.
func (s *S3) PresignedGet(ctx context.Context, p, filename string) (string, error) {
	if s.cfg.Encrypt {
		return "", ErrUnsupported
	}
	q := url.Values{}
	q.Set("response-content-disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(filename)))
	q.Set("response-content-type", "application/octet-stream")
	u, err := s.c.PresignedGetObject(ctx, s.cfg.Bucket, s.key(p), 10*time.Minute, q)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) Exists(ctx context.Context, p string) bool {
	_, err := s.Stat(ctx, p)
	return err == nil
}

func (s *S3) Mkdir(ctx context.Context, p string) error {
	if s.Exists(ctx, p) {
		return ErrExists
	}
	_, err := s.c.PutObject(ctx, s.cfg.Bucket, s.dirKey(p), bytes.NewReader(nil), 0, minio.PutObjectOptions{})
	return friendlyS3(err)
}

func (s *S3) MkdirAll(ctx context.Context, p string) error {
	if p == "." || s.Exists(ctx, p) {
		return nil
	}
	_, err := s.c.PutObject(ctx, s.cfg.Bucket, s.dirKey(p), bytes.NewReader(nil), 0, minio.PutObjectOptions{})
	return friendlyS3(err)
}

// objects lists every key at or below p (the key itself and the prefix p/).
func (s *S3) objects(ctx context.Context, p string) ([]minio.ObjectInfo, error) {
	var out []minio.ObjectInfo
	if info, err := s.c.StatObject(ctx, s.cfg.Bucket, s.key(p), minio.StatObjectOptions{}); err == nil {
		info.Key = s.key(p)
		out = append(out, info)
	}
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.dirKey(p), Recursive: true}) {
		if obj.Err != nil {
			return nil, friendlyS3(obj.Err)
		}
		out = append(out, obj)
	}
	return out, nil
}

func (s *S3) copyKey(ctx context.Context, src, dst string) error {
	_, err := s.c.ComposeObject(ctx,
		minio.CopyDestOptions{Bucket: s.cfg.Bucket, Object: dst},
		minio.CopySrcOptions{Bucket: s.cfg.Bucket, Object: src})
	return friendlyS3(err)
}

// copyTree copies every object under src to dst and returns the source keys.
func (s *S3) copyTree(ctx context.Context, src, dst string) ([]string, error) {
	if dst == src || strings.HasPrefix(dst, src+"/") {
		return nil, ErrIntoSelf
	}
	if s.Exists(ctx, dst) {
		return nil, ErrExists
	}
	objs, err := s.objects(ctx, src)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		return nil, fs.ErrNotExist
	}
	srcKey, dstKey := s.key(src), s.key(dst)
	var keys []string
	for _, o := range objs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		target := dstKey + strings.TrimPrefix(o.Key, srcKey)
		if strings.HasSuffix(o.Key, "/") {
			if _, err := s.c.PutObject(ctx, s.cfg.Bucket, target, bytes.NewReader(nil), 0, minio.PutObjectOptions{}); err != nil {
				return nil, friendlyS3(err)
			}
		} else if err := s.copyKey(ctx, o.Key, target); err != nil {
			return nil, err
		}
		keys = append(keys, o.Key)
	}
	return keys, nil
}

func (s *S3) removeKeys(ctx context.Context, keys []string) error {
	ch := make(chan minio.ObjectInfo)
	go func() {
		defer close(ch)
		for _, k := range keys {
			ch <- minio.ObjectInfo{Key: k}
		}
	}()
	for e := range s.c.RemoveObjects(ctx, s.cfg.Bucket, ch, minio.RemoveObjectsOptions{}) {
		if e.Err != nil {
			return friendlyS3(e.Err)
		}
	}
	return nil
}

func (s *S3) Rename(ctx context.Context, src, dst string) error {
	if src == "." {
		return ErrInvalidPath
	}
	if src == dst {
		return nil
	}
	keys, err := s.copyTree(ctx, src, dst)
	if err != nil {
		return err
	}
	return s.removeKeys(ctx, keys)
}

func (s *S3) Copy(ctx context.Context, src, dst string) error {
	_, err := s.copyTree(ctx, src, dst)
	return err
}

func (s *S3) RemoveAll(ctx context.Context, p string) error {
	if p == "." {
		return ErrInvalidPath
	}
	objs, err := s.objects(ctx, p)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return fs.ErrNotExist
	}
	keys := make([]string, len(objs))
	for i, o := range objs {
		keys[i] = o.Key
	}
	return s.removeKeys(ctx, keys)
}

func (s *S3) WriteFile(ctx context.Context, p string, r io.Reader, size int64) error {
	if s.cfg.Encrypt {
		sr, err := newSealer(s.cfg.Key, nil, 0, size, true, r)
		if err != nil {
			return err
		}
		r, size = sr, cipherSize(size)
	}
	_, err := s.c.PutObject(ctx, s.cfg.Bucket, s.key(p), r, size, minio.PutObjectOptions{ContentType: MimeType(p)})
	return friendlyS3(err)
}

// Walk emits folders (including implied ones) before their contents.
func (s *S3) Walk(ctx context.Context, root string, fn func(Entry) error) error {
	seen := map[string]bool{}
	var skip []string
	emitDir := func(p string) error {
		if seen[p] {
			return nil
		}
		seen[p] = true
		err := fn(dirEntry(p))
		if errors.Is(err, fs.SkipDir) {
			skip = append(skip, p+"/")
			return nil
		}
		return err
	}
	skipped := func(p string) bool {
		for _, s := range skip {
			if strings.HasPrefix(p+"/", s) {
				return true
			}
		}
		return false
	}
	if err := emitDir(root); err != nil {
		return err
	}
	prefix := s.dirKey(root)
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return friendlyS3(obj.Err)
		}
		rel := s.rel(obj.Key)
		if rel == "." || strings.HasPrefix(rel+"/", InternalDir+"/") || skipped(rel) {
			continue
		}
		// Implied parent folders.
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			dp := strings.Join(parts[:i], "/")
			if root != "." && !strings.HasPrefix(dp+"/", root+"/") {
				continue
			}
			if err := emitDir(dp); err != nil {
				return err
			}
			if skipped(rel) {
				break
			}
		}
		if skipped(rel) {
			continue
		}
		if strings.HasSuffix(obj.Key, "/") {
			if err := emitDir(rel); err != nil {
				return err
			}
			continue
		}
		if err := fn(s.objEntry(rel, obj)); err != nil && !errors.Is(err, fs.SkipDir) {
			return err
		}
	}
	return nil
}

func (s *S3) Usage(context.Context) (int64, int64, bool) { return 0, 0, false }

func (s *S3) Trash(ctx context.Context, p, id string) (bool, int64, error) {
	e, err := s.Stat(ctx, p)
	if err != nil {
		return false, 0, err
	}
	if err := s.Rename(ctx, p, trashDir+"/"+id); err != nil {
		return false, 0, err
	}
	return e.IsDir, e.Size, nil
}

func (s *S3) Restore(ctx context.Context, id, orig string) (string, error) {
	dir := Parent(orig)
	dst := join(dir, UniqueName(ctx, s, dir, path.Base(orig)))
	return dst, s.Rename(ctx, trashDir+"/"+id, dst)
}

func (s *S3) PurgeTrash(ctx context.Context, id string) error {
	err := s.RemoveAll(ctx, trashDir+"/"+id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Uploads use S3 multipart uploads straight to the final key, so finishing
// is instant even for huge files.

func (s *S3) ChunkSize() int64 { return s3ChunkSize }

func (s *S3) StartUpload(ctx context.Context, up *UploadState) error {
	target := join(up.Dir, up.Name)
	// A folder is never replaced by a file, even with Overwrite.
	if e, err := s.Stat(ctx, target); err == nil && (!up.Overwrite || e.IsDir) {
		parent := Parent(target)
		target = join(parent, UniqueName(ctx, s, parent, path.Base(target)))
	}
	up.Target = target
	if up.Size == 0 {
		return nil
	}
	id, err := s.core.NewMultipartUpload(ctx, s.cfg.Bucket, s.key(target), minio.PutObjectOptions{ContentType: MimeType(target)})
	if err != nil {
		return friendlyS3(err)
	}
	up.Remote = id
	return nil
}

func (s *S3) UploadOffset(_ context.Context, up *UploadState) (int64, error) {
	var n int64
	for _, p := range up.Parts {
		n += p.Size
	}
	return n, nil
}

func (s *S3) WriteChunk(ctx context.Context, up *UploadState, offset, n int64, r io.Reader) (int64, error) {
	cur, _ := s.UploadOffset(ctx, up)
	if offset != cur {
		return cur, ErrOffset
	}
	if n <= 0 || offset+n > up.Size {
		return cur, ErrTooLarge
	}
	if n != s3ChunkSize && offset+n != up.Size {
		return cur, ErrChunkSize
	}
	body, size := io.Reader(io.LimitReader(r, n)), n
	if s.cfg.Encrypt {
		var nonce0 []byte
		if offset == 0 {
			// A new file key for every try at the first chunk: a retry may
			// carry different bytes, and a nonce must never seal two.
			nonce0 = newNonce()
			up.KeySalt, up.KeyCheck = hex.EncodeToString(nonce0), KeyCheck(s.cfg.Key)
		} else if nonce0, _ = hex.DecodeString(up.KeySalt); len(nonce0) != nonceSize {
			return cur, errors.New("this upload has lost its encryption state: start it again")
		} else if up.KeyCheck != KeyCheck(s.cfg.Key) {
			return cur, errors.New("the drive's encryption key changed during this upload: start it again")
		}
		sr, err := newSealer(s.cfg.Key, nonce0, offset, n, offset+n == up.Size, body)
		if err != nil {
			return cur, err
		}
		body, size = sr, cipherSize(n)
	}
	part, err := s.core.PutObjectPart(ctx, s.cfg.Bucket, s.key(up.Target), up.Remote, len(up.Parts)+1,
		body, size, minio.PutObjectPartOptions{})
	if err != nil {
		return cur, friendlyS3(err)
	}
	up.Parts = append(up.Parts, UploadPart{N: part.PartNumber, ETag: part.ETag, Size: n})
	return cur + n, nil
}

func (s *S3) FinishUpload(ctx context.Context, up *UploadState, trashID string) (string, bool, error) {
	got, _ := s.UploadOffset(ctx, up)
	if got != up.Size {
		return "", false, fmt.Errorf("%w: have %d of %d bytes", ErrOffset, got, up.Size)
	}
	replaced := false
	if up.Overwrite && trashID != "" {
		if e, err := s.Stat(ctx, up.Target); err == nil && !e.IsDir {
			// Keep the old version: copy it to the trash before it is overwritten.
			if err := s.copyKey(ctx, s.key(up.Target), s.key(trashDir+"/"+trashID)); err != nil {
				return "", false, err
			}
			replaced = true
		}
	}
	var err error
	if up.Size == 0 {
		err = s.WriteFile(ctx, up.Target, bytes.NewReader(nil), 0)
	} else {
		parts := make([]minio.CompletePart, len(up.Parts))
		for i, p := range up.Parts {
			parts[i] = minio.CompletePart{PartNumber: p.N, ETag: p.ETag}
		}
		_, err = s.core.CompleteMultipartUpload(ctx, s.cfg.Bucket, s.key(up.Target), up.Remote, parts, minio.PutObjectOptions{})
	}
	if err != nil && replaced {
		// The old file is still in place: drop its copy in the trash.
		if rerr := s.RemoveAll(ctx, trashDir+"/"+trashID); rerr == nil {
			replaced = false
		}
	}
	return up.Target, replaced, friendlyS3(err)
}

func (s *S3) AbortUpload(ctx context.Context, up *UploadState) error {
	if up.Remote == "" {
		return nil
	}
	err := s.core.AbortMultipartUpload(ctx, s.cfg.Bucket, s.key(up.Target), up.Remote)
	if isNotFound(err) {
		return nil
	}
	return friendlyS3(err)
}

// Encrypted drives mark their bucket folder, so another Coffer (or this one
// after a reinstall) knows which key the files need, and encrypted and plain
// files never end up side by side.

const encryptionMarker = InternalDir + "/encryption.json"

// markerVersion is the sealed file format. Version 1 (XChaCha20-Poly1305)
// never shipped in a release.
const markerVersion = 2

type encryptionInfo struct {
	Version  int    `json:"version"`
	Cipher   string `json:"cipher"`
	KeyCheck string `json:"keyCheck"`
}

// NameKey is the key that hides backup object names on an encrypted drive,
// or nil.
func (s *S3) NameKey() []byte {
	if !s.cfg.Encrypt {
		return nil
	}
	return nameKey(s.cfg.Key)
}

func (s *S3) readMarker(ctx context.Context) (*encryptionInfo, error) {
	obj, err := s.c.GetObject(ctx, s.cfg.Bucket, s.key(encryptionMarker), minio.GetObjectOptions{})
	if err != nil {
		return nil, friendlyS3(err)
	}
	defer obj.Close()
	b, err := io.ReadAll(io.LimitReader(obj, 4096))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, friendlyS3(err)
	}
	var info encryptionInfo
	if err := json.Unmarshal(b, &info); err != nil || info.KeyCheck == "" {
		return nil, fmt.Errorf("%s in the bucket is damaged", encryptionMarker)
	}
	switch {
	case info.Version < markerVersion:
		return nil, errors.New("this bucket folder was encrypted by a test build of Coffer that used XChaCha20: choose a new folder")
	case info.Version > markerVersion:
		return nil, errors.New("this bucket folder was encrypted by a newer version of Coffer")
	}
	return &info, nil
}

// firstFile finds a file in the bucket folder, trashed ones included, or
// returns ok false. Folder markers and Coffer's own markers don't count.
func (s *S3) firstFile(ctx context.Context) (info minio.ObjectInfo, ok bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range s.c.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: s.prefix, Recursive: true}) {
		if obj.Err != nil {
			return minio.ObjectInfo{}, false, friendlyS3(obj.Err)
		}
		switch obj.Key {
		case s.key(encryptionMarker), s.key(InternalDir + "/.write-test"):
			continue
		}
		if !strings.HasSuffix(obj.Key, "/") {
			return obj, true, nil
		}
	}
	return minio.ObjectInfo{}, false, nil
}

// sealedWith reports whether the object opens with key: its first segment
// authenticates.
func (s *S3) sealedWith(ctx context.Context, obj minio.ObjectInfo, key []byte) (bool, error) {
	o, err := s.c.GetObject(ctx, s.cfg.Bucket, obj.Key, minio.GetObjectOptions{})
	if err != nil {
		return false, friendlyS3(err)
	}
	r, err := newOpener(key, o, obj.Size)
	if err != nil {
		o.Close()
		return false, nil
	}
	defer r.Close()
	_, err = r.Read(make([]byte, 1))
	switch {
	case err == nil || errors.Is(err, io.EOF):
		return true, nil
	case errors.Is(err, ErrDamaged):
		return false, nil
	}
	return false, friendlyS3(err)
}

func (s *S3) writeMarker(ctx context.Context, key []byte) error {
	b, _ := json.Marshal(encryptionInfo{Version: markerVersion, Cipher: "aes256gcm-64k", KeyCheck: KeyCheck(key)})
	_, err := s.c.PutObject(ctx, s.cfg.Bucket, s.key(encryptionMarker), bytes.NewReader(b), int64(len(b)),
		minio.PutObjectOptions{ContentType: "application/json"})
	return friendlyS3(err)
}

// PrepareEncryption checks that this bucket folder can be used with or
// without encryption, and returns the drive key to use (nil without).
// key is the drive's current key or one from a recovery key, if any.
//
// The files decide: a key is accepted when it matches the folder's marker or
// opens a file in it, and a marker is only ever replaced by a key that does.
// An empty, unmarked folder gets a new key (or the given one). Folders with
// files are never switched between plain and encrypted. With commit, the
// marker is written, or removed from an empty folder that goes plain.
func (s *S3) PrepareEncryption(ctx context.Context, encrypt bool, key []byte, commit bool) ([]byte, error) {
	info, err := s.readMarker(ctx)
	if err != nil {
		return nil, err
	}
	first, has, err := s.firstFile(ctx)
	if err != nil {
		return nil, err
	}
	if !encrypt {
		if info == nil {
			return nil, nil
		}
		if has {
			return nil, errors.New("this bucket folder holds files Coffer encrypted: turn on encryption and enter its recovery key")
		}
		// Left by an encrypted drive that was removed before it stored anything.
		if commit {
			if err := s.c.RemoveObject(ctx, s.cfg.Bucket, s.key(encryptionMarker), minio.RemoveObjectOptions{}); err != nil {
				return nil, friendlyS3(err)
			}
		}
		return nil, nil
	}
	if key == nil {
		switch {
		case info != nil:
			return nil, errors.New("this bucket folder is already set up for encryption: enter its recovery key, or choose a new folder")
		case has:
			return nil, errors.New("this bucket folder already has files that are not encrypted: choose an empty bucket or a new folder in it")
		}
		key = NewDriveKey()
	} else if info == nil || !hmac.Equal([]byte(info.KeyCheck), []byte(KeyCheck(key))) {
		// No marker, or one for another key: only the files can vouch for it.
		switch {
		case has:
			ok, err := s.sealedWith(ctx, first, key)
			if err != nil {
				return nil, err
			}
			if !ok && info != nil {
				return nil, errors.New("that recovery key does not match the files in this bucket folder")
			}
			if !ok {
				return nil, errors.New("this bucket folder already has files that are not encrypted with this key: choose an empty bucket or a new folder in it")
			}
		case info != nil:
			return nil, errors.New("this bucket folder is set up for encryption with another key: enter that recovery key, or choose a new folder")
		}
	} else {
		return key, nil // the marker already names this key
	}
	if commit {
		if err := s.writeMarker(ctx, key); err != nil {
			return nil, err
		}
	}
	return key, nil
}
