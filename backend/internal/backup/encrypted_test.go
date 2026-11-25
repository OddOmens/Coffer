package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
)

// Backups into an encrypted bucket: the bucket sees no file names, contents
// or manifest, and restores and pruning work as usual. Needs
// COFFER_TEST_S3_ENDPOINT.
func TestBackupToEncryptedBucket(t *testing.T) {
	ep := os.Getenv("COFFER_TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("set COFFER_TEST_S3_ENDPOINT, _BUCKET, _KEY and _SECRET to run")
	}
	cfg := files.S3Config{
		Endpoint: ep, Bucket: os.Getenv("COFFER_TEST_S3_BUCKET"), Region: "us-east-1",
		Prefix:    "coffer-test-" + strings.ToLower(security.RandomToken(6)),
		AccessKey: os.Getenv("COFFER_TEST_S3_KEY"), SecretKey: os.Getenv("COFFER_TEST_S3_SECRET"),
		Insecure: true, PathStyle: true,
	}
	raw, err := files.OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.RemoveAll(context.Background(), "backups") })
	cfg.Encrypt, cfg.Key = true, files.NewDriveKey()
	dst, err := files.OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	src, srcDir := local(t)
	t0 := time.Unix(1_700_000_000, 0)
	write(t, srcDir, "Clients/Acme/contract.txt", "the secret terms", t0)
	write(t, srcDir, "Clients/Acme/notes.txt", "more secret notes", t0)
	snap1, pr, err := Run(ctx, src, ".", nil, dst, "backups", nil)
	if err != nil || pr.Uploaded != 2 {
		t.Fatalf("run: %+v %v", pr, err)
	}

	plainName := sha256.Sum256([]byte("Clients/Acme/contract.txt"))
	var objects int
	err = raw.Walk(ctx, "backups", func(e files.Entry) error {
		if e.IsDir {
			return nil
		}
		if strings.Contains(e.Name, hex.EncodeToString(plainName[:])) {
			t.Errorf("%s: object named by the bare path hash", e.Path)
		}
		rc, _, err := raw.Open(ctx, e.Path)
		if err != nil {
			return err
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if bytes.Contains(b, []byte("secret")) || bytes.Contains(b, []byte("Acme")) || bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
			t.Errorf("%s readable in the bucket", e.Path)
		}
		if strings.HasPrefix(e.Path, "backups/objects/") {
			objects++
		}
		return nil
	})
	if err != nil || objects != 2 {
		t.Fatalf("walk: %d objects, %v", objects, err)
	}

	// Unchanged files are found again under their keyed names.
	write(t, srcDir, "Clients/Acme/notes.txt", "edited notes", t0.Add(time.Hour))
	time.Sleep(1100 * time.Millisecond)
	snap2, pr, err := Run(ctx, src, ".", nil, dst, "backups", nil)
	if err != nil || pr.Uploaded != 1 {
		t.Fatalf("second run: %+v %v", pr, err)
	}
	m1, err := Load(ctx, dst, "backups", snap1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := Restore(ctx, dst, "backups", m1, []string{"."}, src, "Restored"); err != nil || n != 2 {
		t.Fatalf("restore: %d %v", n, err)
	}
	if b := read(t, src, "Restored/Clients/Acme/notes.txt"); b != "more secret notes" {
		t.Fatalf("restored %q", b)
	}
	if rs, ro, err := Prune(ctx, dst, "backups", 1); err != nil || rs != 1 || ro != 1 {
		t.Fatalf("prune: %d %d %v", rs, ro, err)
	}
	if _, err := Load(ctx, dst, "backups", snap2); err != nil {
		t.Fatal(err)
	}
}
