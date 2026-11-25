package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/minio/minio-go/v7"
)

// testS3Config is a fresh folder in the test bucket, or skips the test when
// COFFER_TEST_S3_ENDPOINT is not set (e.g. MinIO in CI or locally).
func testS3Config(t *testing.T) S3Config {
	t.Helper()
	ep := os.Getenv("COFFER_TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("set COFFER_TEST_S3_ENDPOINT, _BUCKET, _KEY and _SECRET to run")
	}
	cfg := S3Config{
		Endpoint: ep, Bucket: os.Getenv("COFFER_TEST_S3_BUCKET"), Region: "us-east-1", Prefix: "coffer-test-" + RandomTestID(),
		AccessKey: os.Getenv("COFFER_TEST_S3_KEY"), SecretKey: os.Getenv("COFFER_TEST_S3_SECRET"),
		Insecure: true, PathStyle: true,
	}
	d, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.c.BucketExists(t.Context(), d.cfg.Bucket); !ok {
		if err := d.c.MakeBucket(t.Context(), d.cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

func TestS3Drive(t *testing.T) {
	t.Run("plain", func(t *testing.T) { testS3Drive(t, false) })
	t.Run("encrypted", func(t *testing.T) { testS3Drive(t, true) })
}

func testS3Drive(t *testing.T, encrypt bool) {
	cfg := testS3Config(t)
	raw, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if encrypt {
		cfg.Encrypt, cfg.Key, cfg.DirectDownloads = true, NewDriveKey(), true
	}
	d, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	_ = d.RemoveAll(ctx, "work")
	t.Cleanup(func() { d.RemoveAll(t.Context(), "work"); d.RemoveAll(t.Context(), InternalDir) })

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.MkdirAll(ctx, "work"))
	must(d.Mkdir(ctx, "work/empty"))
	if err := d.Mkdir(ctx, "work/empty"); err != ErrExists {
		t.Fatalf("mkdir twice: %v", err)
	}
	must(d.WriteFile(ctx, "work/a/b/note.txt", strings.NewReader("hello"), 5))
	if e, err := d.Stat(ctx, "work/a"); err != nil || !e.IsDir {
		t.Fatalf("implied folder: %+v %v", e, err)
	}
	es, err := d.List(ctx, "work", false)
	must(err)
	if len(es) != 2 || !es[0].IsDir || !es[1].IsDir {
		t.Fatalf("list: %+v", es)
	}

	// Multipart upload: one full 16 MiB part plus a short last part.
	payload := make([]byte, s3ChunkSize+1234)
	rand.Read(payload)
	up := &UploadState{ID: "upload-test-0000001", Size: int64(len(payload)), Dir: "work", Name: "big/render.mov"}
	must(d.StartUpload(ctx, up))
	if _, err := d.WriteChunk(ctx, up, 0, 100, bytes.NewReader(payload[:100])); err != ErrChunkSize {
		t.Fatalf("short middle chunk accepted: %v", err)
	}
	off, err := d.WriteChunk(ctx, up, 0, s3ChunkSize, bytes.NewReader(payload[:s3ChunkSize]))
	must(err)
	off, err = d.WriteChunk(ctx, up, off, int64(len(payload))-off, bytes.NewReader(payload[off:]))
	must(err)
	final, _, err := d.FinishUpload(ctx, up, "")
	must(err)
	if final != "work/big/render.mov" {
		t.Fatalf("final path %q", final)
	}
	if encrypt {
		// The bucket only ever sees ciphertext, at the sealed size.
		for p, plain := range map[string][]byte{"work/a/b/note.txt": []byte("hello"), final: payload} {
			rc, e, err := raw.Open(ctx, p)
			must(err)
			stored, _ := io.ReadAll(rc)
			rc.Close()
			if e.Size != cipherSize(int64(len(plain))) || bytes.Contains(stored, plain[:5]) {
				t.Fatalf("%s stored as %d bytes, plaintext visible: %v", p, e.Size, bytes.Contains(stored, plain[:5]))
			}
		}
		if _, err := d.PresignedGet(ctx, final, "render.mov"); err == nil || d.Config().DirectDownloads {
			t.Fatal("direct downloads would hand out ciphertext")
		}
	}
	if e, err := d.Stat(ctx, final); err != nil || e.Size != int64(len(payload)) {
		t.Fatalf("stat: %+v %v", e, err)
	}
	rc, e, err := d.Open(ctx, final)
	must(err)
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, payload) || e.Size != int64(len(payload)) {
		t.Fatal("multipart content mismatch")
	}
	// Range reads (video scrubbing) through Seek.
	rc, _, _ = d.Open(ctx, final)
	rc.Seek(s3ChunkSize, io.SeekStart)
	tail, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(tail, payload[s3ChunkSize:]) {
		t.Fatal("seek read mismatch")
	}

	// Rename a folder, copy it, trash and restore.
	must(d.Rename(ctx, "work/a", "work/renamed"))
	if d.Exists(ctx, "work/a/b/note.txt") || !d.Exists(ctx, "work/renamed/b/note.txt") {
		t.Fatal("rename did not move the tree")
	}
	must(d.Copy(ctx, "work/renamed", "work/copy"))
	if !d.Exists(ctx, "work/copy/b/note.txt") || !d.Exists(ctx, "work/renamed/b/note.txt") {
		t.Fatal("copy wrong")
	}
	isDir, _, err := d.Trash(ctx, "work/copy", "trash-test-1")
	must(err)
	if !isDir || d.Exists(ctx, "work/copy") {
		t.Fatal("trash did not move folder")
	}
	back, err := d.Restore(ctx, "trash-test-1", "work/copy")
	must(err)
	if back != "work/copy" || !d.Exists(ctx, "work/copy/b/note.txt") {
		t.Fatalf("restore: %s", back)
	}

	var walked []string
	must(d.Walk(ctx, "work", func(e Entry) error { walked = append(walked, e.Path); return nil }))
	joined := strings.Join(walked, ",")
	for _, want := range []string{"work", "work/big", "work/big/render.mov", "work/copy/b/note.txt", "work/empty"} {
		if !strings.Contains(","+joined+",", ","+want+",") {
			t.Fatalf("walk missing %s: %v", want, walked)
		}
	}
	must(d.RemoveAll(ctx, "work/renamed"))
	if d.Exists(ctx, "work/renamed") {
		t.Fatal("remove failed")
	}

	// Empty files, written whole and through an upload.
	must(d.WriteFile(ctx, "work/empty.txt", bytes.NewReader(nil), 0))
	up = &UploadState{ID: "upload-test-0000002", Size: 0, Dir: "work", Name: "empty2.txt"}
	must(d.StartUpload(ctx, up))
	_, _, err = d.FinishUpload(ctx, up, "")
	must(err)
	for _, p := range []string{"work/empty.txt", "work/empty2.txt"} {
		rc, e, err := d.Open(ctx, p)
		must(err)
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || len(got) != 0 || e.Size != 0 {
			t.Fatalf("%s: %d bytes, size %d, %v", p, len(got), e.Size, err)
		}
	}

	// Replacing a file through an upload keeps the old version in the trash.
	up = &UploadState{ID: "upload-test-0000003", Size: 3, Dir: "work", Name: "empty.txt", Overwrite: true}
	must(d.StartUpload(ctx, up))
	_, err = d.WriteChunk(ctx, up, 0, 3, strings.NewReader("new"))
	must(err)
	_, replaced, err := d.FinishUpload(ctx, up, "trash-test-2")
	must(err)
	if !replaced {
		t.Fatal("old version not kept")
	}
	for p, want := range map[string]string{"work/empty.txt": "new", trashDir + "/trash-test-2": ""} {
		rc, _, err := d.Open(ctx, p)
		must(err)
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", p, got, err)
		}
	}
	must(d.PurgeTrash(ctx, "trash-test-2"))
}

func TestS3RegionFromEndpoint(t *testing.T) {
	for ep, want := range map[string]string{
		"https://s3.eu-central-003.backblazeb2.com/": "eu-central-003",
		"S3.US-EAST-005.BackblazeB2.com":             "us-east-005",
		"s3.us-west-2.amazonaws.com":                 "us-west-2",
		"s3-us-west-2.amazonaws.com":                 "us-west-2",
		"s3.amazonaws.com":                           "keep",
		"acct.r2.cloudflarestorage.com":              "keep",
		"minio.local:9000":                           "keep",
	} {
		d, err := OpenS3(S3Config{Endpoint: ep, Region: "keep", Bucket: "b", AccessKey: "a", SecretKey: "s"})
		if err != nil || d.Config().Region != want {
			t.Errorf("%s: region %q, want %q (%v)", ep, d.Config().Region, want, err)
		}
	}
	d, _ := OpenS3(S3Config{Endpoint: " s3.us-east-005.backblazeb2.com\n", Bucket: " b ", AccessKey: "005abc \n", SecretKey: "\tK005xyz "})
	if c := d.Config(); c.AccessKey != "005abc" || c.SecretKey != "K005xyz" || c.Bucket != "b" || c.Region != "us-east-005" {
		t.Fatalf("pasted values not trimmed: %+v", c)
	}
}

// A first chunk that fails part way is sent again, possibly with other bytes.
// Each try must seal under a new file key.
func TestS3EncryptedChunkRetry(t *testing.T) {
	cfg := testS3Config(t)
	cfg.Encrypt, cfg.Key = true, NewDriveKey()
	d, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	t.Cleanup(func() { d.RemoveAll(context.Background(), "f.bin") })
	up := &UploadState{ID: "retry", Size: s3ChunkSize + 10, Dir: ".", Name: "f.bin"}
	if err := d.StartUpload(ctx, up); err != nil {
		t.Fatal(err)
	}
	first := randBytes(s3ChunkSize)
	if _, err := d.WriteChunk(ctx, up, 0, s3ChunkSize, io.MultiReader(bytes.NewReader(first[:1000]), iotest.ErrReader(errors.New("dropped")))); err == nil {
		t.Fatal("broken chunk accepted")
	}
	salt := up.KeySalt
	up.Parts = nil
	if _, err := d.WriteChunk(ctx, up, 0, s3ChunkSize, bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	if up.KeySalt == salt {
		t.Fatal("first chunk retried under the same file key")
	}
	if _, err := d.WriteChunk(ctx, up, s3ChunkSize, 10, strings.NewReader("0123456789")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.FinishUpload(ctx, up, ""); err != nil {
		t.Fatal(err)
	}
	rc, _, err := d.Open(ctx, "f.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, append(first, "0123456789"...)) {
		t.Fatalf("read back: %v", err)
	}
}

func TestS3PrepareEncryption(t *testing.T) {
	cfg := testS3Config(t)
	d, err := OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	t.Cleanup(func() {
		d.RemoveAll(context.Background(), "f.txt")
		d.RemoveAll(context.Background(), InternalDir)
	})
	wantErr := func(err error, sub string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), sub) {
			t.Fatalf("got %v, want an error about %q", err, sub)
		}
	}
	marker := func() *encryptionInfo {
		t.Helper()
		info, err := d.readMarker(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}

	// An empty folder: a test run writes nothing, adding it does.
	key, err := d.PrepareEncryption(ctx, true, nil, false)
	if err != nil || len(key) != DriveKeySize || marker() != nil {
		t.Fatal(key, err, "or a test run marked the folder")
	}
	key, err = d.PrepareEncryption(ctx, true, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if info := marker(); info == nil || info.KeyCheck != KeyCheck(key) {
		t.Fatalf("marker: %+v", info)
	}

	// Marked but still empty: a second drive must not get its own key.
	_, err = d.PrepareEncryption(ctx, true, nil, true)
	wantErr(err, "enter its recovery key")
	_, err = d.PrepareEncryption(ctx, true, NewDriveKey(), true)
	wantErr(err, "another key")
	if got, err := d.PrepareEncryption(ctx, true, bytes.Clone(key), true); err != nil || !bytes.Equal(got, key) {
		t.Fatal(got, err)
	}
	if marker().KeyCheck != KeyCheck(key) {
		t.Fatal("marker replaced")
	}

	enc := cfg
	enc.Encrypt, enc.Key = true, key
	ed, _ := OpenS3(enc)
	if err := ed.WriteFile(ctx, "f.txt", strings.NewReader("secret"), 6); err != nil {
		t.Fatal(err)
	}
	_, err = d.PrepareEncryption(ctx, true, nil, false)
	wantErr(err, "enter its recovery key")
	_, err = d.PrepareEncryption(ctx, true, NewDriveKey(), false)
	wantErr(err, "does not match")
	_, err = d.PrepareEncryption(ctx, false, nil, true)
	wantErr(err, "turn on encryption")

	// The marker is lost: the right key still opens the files and marks the
	// folder again; a wrong one doesn't.
	if err := d.c.RemoveObject(ctx, cfg.Bucket, d.key(encryptionMarker), minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = d.PrepareEncryption(ctx, true, NewDriveKey(), true)
	wantErr(err, "not encrypted with this key")
	_, err = d.PrepareEncryption(ctx, true, nil, true)
	wantErr(err, "not encrypted")
	if got, err := d.PrepareEncryption(ctx, true, bytes.Clone(key), true); err != nil || !bytes.Equal(got, key) {
		t.Fatal(got, err)
	}
	if info := marker(); info == nil || info.KeyCheck != KeyCheck(key) {
		t.Fatal("marker not restored")
	}

	// Trashed files count: their key must not be thrown away either.
	if _, _, err := ed.Trash(ctx, "f.txt", "t1"); err != nil {
		t.Fatal(err)
	}
	_, err = d.PrepareEncryption(ctx, false, nil, true)
	wantErr(err, "turn on encryption")
	if err := ed.PurgeTrash(ctx, "t1"); err != nil {
		t.Fatal(err)
	}

	// Empty again: it can go plain, and the leftover marker goes.
	if k, err := d.PrepareEncryption(ctx, false, nil, true); err != nil || k != nil || marker() != nil {
		t.Fatal(k, err, "or the marker stayed")
	}

	// A folder from the XChaCha20 test builds is refused, not misread.
	old := []byte(`{"version":1,"cipher":"xchacha20poly1305-64k","keyCheck":"x"}`)
	if _, err := d.c.PutObject(ctx, cfg.Bucket, d.key(encryptionMarker), bytes.NewReader(old), int64(len(old)), minio.PutObjectOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = d.PrepareEncryption(ctx, true, nil, false)
	wantErr(err, "XChaCha20")
	if err := d.c.RemoveObject(ctx, cfg.Bucket, d.key(encryptionMarker), minio.RemoveObjectOptions{}); err != nil {
		t.Fatal(err)
	}

	// A folder with plain files never becomes encrypted.
	plain := testS3Config(t)
	pd, _ := OpenS3(plain)
	t.Cleanup(func() { pd.RemoveAll(context.Background(), "p.txt") })
	if err := pd.WriteFile(ctx, "p.txt", strings.NewReader("plain"), 5); err != nil {
		t.Fatal(err)
	}
	_, err = pd.PrepareEncryption(ctx, true, nil, true)
	wantErr(err, "not encrypted")
	_, err = pd.PrepareEncryption(ctx, true, NewDriveKey(), true)
	wantErr(err, "not encrypted")
	if k, err := pd.PrepareEncryption(ctx, false, nil, true); err != nil || k != nil {
		t.Fatal(k, err)
	}
	if _, err := OpenS3(S3Config{Endpoint: "x", Bucket: "b", AccessKey: "a", SecretKey: "s", Encrypt: true}); err == nil {
		t.Fatal("encrypted drive opened without a key")
	}
}

// An upload started under one drive key can't be finished under another.
func TestS3UploadKeyChange(t *testing.T) {
	cfg := testS3Config(t)
	cfg.Encrypt, cfg.Key = true, NewDriveKey()
	d, _ := OpenS3(cfg)
	ctx := t.Context()
	up := &UploadState{ID: "rekey", Size: s3ChunkSize + 1, Dir: ".", Name: "f.bin"}
	if err := d.StartUpload(ctx, up); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.AbortUpload(context.Background(), up) })
	if _, err := d.WriteChunk(ctx, up, 0, s3ChunkSize, bytes.NewReader(make([]byte, s3ChunkSize))); err != nil {
		t.Fatal(err)
	}
	cfg.Key = NewDriveKey()
	d2, _ := OpenS3(cfg)
	if _, err := d2.WriteChunk(ctx, up, s3ChunkSize, 1, strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "key changed") {
		t.Fatalf("chunk under a new key: %v", err)
	}
}

// RandomTestID gives each run its own prefix; some S3 servers keep empty
// folder prefixes from earlier runs.
func RandomTestID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return strings.ToLower(strings.TrimRight(b32.EncodeToString(b), "="))
}

var b32 = base32.StdEncoding
