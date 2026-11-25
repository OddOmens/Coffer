package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/oddomens/coffer/internal/backup"
	"github.com/oddomens/coffer/internal/files"
	"github.com/oddomens/coffer/internal/security"
)

// testS3 is a fresh folder in the test bucket, or skips the test when
// COFFER_TEST_S3_ENDPOINT is not set.
func testS3(t *testing.T) files.S3Config {
	t.Helper()
	ep := os.Getenv("COFFER_TEST_S3_ENDPOINT")
	if ep == "" {
		t.Skip("set COFFER_TEST_S3_ENDPOINT, _BUCKET, _KEY and _SECRET to run")
	}
	return files.S3Config{
		Endpoint: ep, Bucket: os.Getenv("COFFER_TEST_S3_BUCKET"), Region: "us-east-1",
		Prefix:    "coffer-test-" + strings.ToLower(security.RandomToken(6)),
		AccessKey: os.Getenv("COFFER_TEST_S3_KEY"), SecretKey: os.Getenv("COFFER_TEST_S3_SECRET"),
		Insecure: true, PathStyle: true,
	}
}

func TestEncryptedDrive(t *testing.T) {
	cfg := testS3(t)
	raw, err := files.OpenS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw.RemoveAll(context.Background(), "plans.txt")
		raw.RemoveAll(context.Background(), "backups")
		raw.RemoveAll(context.Background(), files.InternalDir)
	})
	cfg.Encrypt, cfg.DirectDownloads = true, true

	e := newEnv(t)
	c := e.client(e.app.URL)
	c.login("admin")
	resp, body := c.do("POST", "/api/admin/drives", map[string]any{"name": "Vault", "kind": "s3", "s3": cfg})
	expect(t, resp, body, 201)
	var d struct {
		ID          string
		RecoveryKey string
		S3          files.S3Config
	}
	json.Unmarshal(body, &d)
	if d.RecoveryKey == "" || !d.S3.Encrypt || d.S3.DirectDownloads {
		t.Fatalf("created: %s", body)
	}
	place := drivePlacePrefix + d.ID

	// A resumable upload, then whole and ranged downloads, all in plain.
	data := bytes.Repeat([]byte("confidential plans "), 10_000) // several segments
	resp, body = c.do("POST", "/api/uploads", map[string]any{"place": place, "dir": "/", "name": "plans.txt", "size": len(data)})
	expect(t, resp, body, 201)
	var up struct{ ID string }
	json.Unmarshal(body, &up)
	resp, body = c.do("PATCH", "/api/uploads/"+up.ID, data, "Upload-Offset", "0")
	expect(t, resp, body, 200)
	resp, body = c.do("POST", "/api/uploads/"+up.ID+"/complete", nil)
	expect(t, resp, body, 200)

	resp, body = c.do("GET", "/api/files/download?"+q(place, "/plans.txt"), nil)
	expect(t, resp, body, 200) // never a redirect to the bucket
	if !bytes.Equal(body, data) {
		t.Fatal("download differs")
	}
	resp, body = c.do("GET", "/api/files/download?inline=1&"+q(place, "/plans.txt"), nil, "Range", "bytes=70000-70009")
	expect(t, resp, body, 206)
	if !bytes.Equal(body, data[70000:70010]) {
		t.Fatalf("range: %q", body)
	}
	resp, body = c.do("GET", "/api/files/stat?"+q(place, "/plans.txt"), nil)
	if !strings.Contains(string(body), `"size":190000`) {
		t.Fatalf("stat: %s", body)
	}

	rc, _, err := raw.Open(t.Context(), "plans.txt")
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(rc)
	rc.Close()
	if bytes.Contains(stored, []byte("confidential")) {
		t.Fatal("plaintext in the bucket")
	}

	// A backup of the main drive into the encrypted one.
	src, _ := e.srv.drive("files")
	vault, _ := e.srv.drive(d.ID)
	if _, _, err := backup.Run(t.Context(), src.d, ".", nil, vault.d, "backups", nil); err != nil {
		t.Fatal(err)
	}

	// A file put in the bucket some other way is refused with a clear reason.
	if err := raw.WriteFile(t.Context(), "dropped-in.txt", strings.NewReader("not sealed"), 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.RemoveAll(context.Background(), "dropped-in.txt") })
	resp, body = c.do("GET", "/api/files/download?"+q(place, "/dropped-in.txt"), nil)
	if resp.StatusCode != 500 || !strings.Contains(string(body), `"damaged"`) {
		t.Fatalf("damaged file: %d %s", resp.StatusCode, body)
	}

	// Everyone can see which drives are encrypted.
	resp, body = c.do("GET", "/api/places", nil)
	if !strings.Contains(string(body), `"encrypted":true`) {
		t.Fatalf("places: %s", body)
	}

	// Neither the drive list nor the stored config gives a key away.
	resp, body = c.do("GET", "/api/admin/drives", nil)
	for _, secret := range []string{d.RecoveryKey, cfg.SecretKey, "keySealed", "secretSealed"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("drive list shows %q", secret)
		}
	}

	// The recovery key again, only with the password.
	resp, body = c.do("POST", "/api/admin/drives/"+d.ID+"/recovery-key", map[string]string{"password": "wrong"})
	expect(t, resp, body, 403)
	resp, body = c.do("POST", "/api/admin/drives/"+d.ID+"/recovery-key", map[string]string{"password": "correct horse battery"})
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), d.RecoveryKey) {
		t.Fatalf("recovery key: %s", body)
	}

	// Encryption stays as chosen.
	plainCfg := cfg
	plainCfg.Encrypt = false
	resp, body = c.do("PATCH", "/api/admin/drives/"+d.ID, map[string]any{"s3": plainCfg})
	expect(t, resp, body, 400)
	resp, body = c.do("PATCH", "/api/admin/drives/"+d.ID, map[string]any{"s3": cfg})
	expect(t, resp, body, 200)
	if strings.Contains(string(body), "recoveryKey") {
		t.Fatal("saving settings made a new key")
	}

	// No second drive on this folder, or inside it, with its own key or none.
	nested := cfg
	nested.Prefix += "/sub"
	nestedPlain := nested
	nestedPlain.Encrypt = false
	for _, req := range []map[string]any{
		{"name": "Again", "kind": "s3", "s3": cfg},
		{"name": "Inside", "kind": "s3", "s3": nested},
		{"name": "Inside", "kind": "s3", "s3": nestedPlain},
	} {
		resp, body = c.do("POST", "/api/admin/drives", req)
		expect(t, resp, body, 400)
	}

	// A new server (its own instance key) can only read the bucket with the
	// recovery key, and testing the connection changes nothing.
	e2 := newEnv(t)
	c2 := e2.client(e2.app.URL)
	c2.login("admin")
	for _, req := range []map[string]any{
		{"name": "Vault", "kind": "s3", "s3": plainCfg},
		{"name": "Vault", "kind": "s3", "s3": cfg},
		{"name": "Vault", "kind": "s3", "s3": cfg, "recoveryKey": files.FormatRecoveryKey(files.NewDriveKey())},
		{"name": "Vault", "kind": "s3", "s3": cfg, "recoveryKey": d.RecoveryKey + "A"},
	} {
		resp, body = c2.do("POST", "/api/admin/drives", req)
		expect(t, resp, body, 400)
	}
	resp, body = c2.do("POST", "/api/admin/drives/test", map[string]any{"kind": "s3", "s3": cfg, "recoveryKey": d.RecoveryKey})
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("test with the recovery key: %s", body)
	}
	sloppy := strings.ToLower(strings.ReplaceAll(d.RecoveryKey, "-", " "))
	resp, body = c2.do("POST", "/api/admin/drives", map[string]any{"name": "Vault", "kind": "s3", "s3": cfg, "recoveryKey": sloppy})
	expect(t, resp, body, 201)
	if strings.Contains(string(body), "recoveryKey") {
		t.Fatal("asked to save a key the admin just typed in")
	}
	var d2 struct{ ID string }
	json.Unmarshal(body, &d2)
	resp, body = c2.do("GET", "/api/files/download?"+q(drivePlacePrefix+d2.ID, "/plans.txt"), nil)
	expect(t, resp, body, 200)
	if !bytes.Equal(body, data) {
		t.Fatal("recovered download differs")
	}

	// A backup job on the same target folder picks up the old snapshots.
	resp, body = c2.do("POST", "/api/admin/backups", map[string]any{"name": "Studio", "sourceDrive": "files", "sourcePath": "/",
		"targetDrive": d2.ID, "targetPath": "backups"})
	expect(t, resp, body, 201)
	var job struct{ ID string }
	json.Unmarshal(body, &job)
	resp, body = c2.do("GET", "/api/admin/backups/"+job.ID+"/snapshots", nil)
	expect(t, resp, body, 200)
	var snaps []struct{ Name string }
	json.Unmarshal(body, &snaps)
	if len(snaps) != 1 {
		t.Fatalf("snapshots: %s", body)
	}
	resp, body = c2.do("GET", "/api/admin/backups/"+job.ID+"/snapshots/"+snaps[0].Name, nil)
	expect(t, resp, body, 200)
	if !strings.Contains(string(body), "projects") {
		t.Fatalf("snapshot contents: %s", body)
	}
}
