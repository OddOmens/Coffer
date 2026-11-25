package files

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"strings"
	"testing"
)

type memObject struct {
	*bytes.Reader
	seeks int
}

func (m *memObject) Seek(off int64, whence int) (int64, error) {
	m.seeks++
	return m.Reader.Seek(off, whence)
}

func (m *memObject) Close() error { return nil }

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func seal(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	sr, err := newSealer(key, nil, 0, int64(len(plain)), true, bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	c, err := io.ReadAll(sr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func open(t *testing.T, key, c []byte) (*opener, *memObject) {
	t.Helper()
	obj := &memObject{Reader: bytes.NewReader(c)}
	o, err := newOpener(key, obj, int64(len(c)))
	if err != nil {
		t.Fatal(err)
	}
	return o, obj
}

var sealSizes = []int{0, 1, 2, segPlain - 1, segPlain, segPlain + 1, 2 * segPlain, 3*segPlain + 7, 200_000}

func TestChunksHoldWholeSegments(t *testing.T) {
	if s3ChunkSize%segPlain != 0 {
		t.Fatal("upload chunks must hold whole segments")
	}
	if got := cipherSize(s3ChunkSize); got != s3ChunkSize/segPlain*segCipher {
		t.Fatalf("full chunk seals to %d", got)
	}
}

func TestSealedSizes(t *testing.T) {
	sizes := []int64{0, 1, segPlain - 1, segPlain, segPlain + 1, 5 * segPlain, 5*segPlain + 1, 1 << 40}
	for range 1000 {
		sizes = append(sizes, mrand.Int64N(50*segPlain))
	}
	for _, n := range sizes {
		if got, ok := plainSize(cipherSize(n)); !ok || got != n {
			t.Fatalf("size %d: round trip gave %d %v", n, got, ok)
		}
	}
	// Sizes no sealed file can have: shorter than one empty segment, or an
	// empty segment after full ones.
	for _, c := range []int64{1, segOverhead - 1, segCipher + 1, segCipher + segOverhead, 2*segCipher + segOverhead} {
		if _, ok := plainSize(c); ok {
			t.Fatalf("size %d accepted", c)
		}
	}
	key := NewDriveKey()
	for _, n := range sealSizes {
		if c := seal(t, key, randBytes(n)); int64(len(c)) != cipherSize(int64(n)) {
			t.Fatalf("%d bytes sealed to %d, want %d", n, len(c), cipherSize(int64(n)))
		}
	}
}

func TestSealRoundTrip(t *testing.T) {
	key := NewDriveKey()
	for _, n := range sealSizes {
		plain := randBytes(n)
		c := seal(t, key, plain)
		if n > 16 && bytes.Contains(c, plain[:16]) {
			t.Fatalf("%d: plaintext visible in ciphertext", n)
		}
		o, obj := open(t, key, c)
		got, err := io.ReadAll(o)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: read back %d bytes, %v", n, len(got), err)
		}
		if obj.seeks != 0 {
			t.Fatalf("%d bytes: a straight read seeked %d times", n, obj.seeks)
		}
		if end, _ := o.Seek(0, io.SeekEnd); end != int64(n) {
			t.Fatalf("%d bytes: SeekEnd gave %d", n, end)
		}
		// Ranges, the way http.ServeContent asks for them.
		for range 40 {
			if n == 0 {
				break
			}
			off := mrand.IntN(n)
			l := min(mrand.IntN(3*segPlain)+1, n-off)
			if _, err := o.Seek(int64(off), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, l)
			if _, err := io.ReadFull(o, buf); err != nil || !bytes.Equal(buf, plain[off:off+l]) {
				t.Fatalf("%d bytes: range %d+%d wrong: %v", n, off, l, err)
			}
		}
		o.Seek(int64(n)+10, io.SeekStart)
		if k, err := o.Read(make([]byte, 1)); k != 0 || err != io.EOF {
			t.Fatalf("read past end: %d %v", k, err)
		}
	}
}

// Uploads seal chunk by chunk; the result must read like a file sealed at once.
func TestSealChunks(t *testing.T) {
	key := NewDriveKey()
	const chunk = 2 * segPlain
	for _, n := range []int{1, chunk, chunk + 1, 3 * chunk, 3*chunk + segPlain/2} {
		plain := randBytes(n)
		nonce0 := newNonce()
		var c []byte
		for off := 0; off < n; off += chunk {
			end := min(off+chunk, n)
			sr, err := newSealer(key, nonce0, int64(off), int64(end-off), end == n, bytes.NewReader(plain[off:end]))
			if err != nil {
				t.Fatal(err)
			}
			part, err := io.ReadAll(sr)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(part)) != cipherSize(int64(end-off)) {
				t.Fatalf("part sealed to %d, want %d", len(part), cipherSize(int64(end-off)))
			}
			c = append(c, part...)
		}
		o, _ := open(t, key, c)
		if got, err := io.ReadAll(o); err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes in chunks: %v", n, err)
		}
	}
	if _, err := newSealer(key, newNonce(), 100, 10, true, nil); err == nil {
		t.Fatal("unaligned chunk accepted")
	}
	if _, err := newSealer(key, nil, segPlain, 10, true, nil); err == nil {
		t.Fatal("later chunk without the file nonce accepted")
	}
}

func TestSealShortSource(t *testing.T) {
	sr, _ := newSealer(NewDriveKey(), nil, 0, 3*segPlain, true, bytes.NewReader(make([]byte, segPlain+5)))
	if _, err := io.ReadAll(sr); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short source: %v", err)
	}
	// Bytes past the declared size are never read.
	src := strings.NewReader("abcdefgh")
	sr, _ = newSealer(NewDriveKey(), nil, 0, 3, true, src)
	io.ReadAll(sr)
	if src.Len() != 5 {
		t.Fatalf("read %d bytes past the size", 5-src.Len())
	}
}

func TestSealFreshNonces(t *testing.T) {
	key := NewDriveKey()
	plain := make([]byte, 4*segPlain)
	a, b := seal(t, key, plain), seal(t, key, plain)
	if bytes.Equal(a, b) {
		t.Fatal("same plaintext sealed twice gave the same ciphertext")
	}
	seen := map[string]bool{}
	for _, c := range [][]byte{a, b} {
		for off := 0; off < len(c); off += segCipher {
			n := string(c[off : off+nonceSize])
			if seen[n] {
				t.Fatal("nonce used twice")
			}
			seen[n] = true
		}
	}
}

func readAllErr(t *testing.T, key, c []byte) error {
	t.Helper()
	o, err := newOpener(key, &memObject{Reader: bytes.NewReader(c)}, int64(len(c)))
	if err != nil {
		return err
	}
	_, err = io.ReadAll(o)
	return err
}

func TestSealTamper(t *testing.T) {
	key := NewDriveKey()
	plain := randBytes(3*segPlain + 100) // four segments, the last short
	c := seal(t, key, plain)
	seg := func(i int) []byte { return c[i*segCipher : min((i+1)*segCipher, len(c))] }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	other := seal(t, key, randBytes(len(plain)))

	cases := map[string][]byte{
		"wrong key":                 nil,
		"last segment dropped":      c[:3*segCipher],
		"cut inside a segment":      c[:2*segCipher+500],
		"segments swapped":          cat(seg(1), seg(0), seg(2), seg(3)),
		"segment from another file": cat(seg(0), other[segCipher:2*segCipher], seg(2), seg(3)),
		"segment appended":          cat(c, other[segCipher:2*segCipher]),
	}
	for i := range 4 {
		for _, at := range []int{0, nonceSize, len(seg(i)) - 1} { // nonce, ciphertext, tag
			b := bytes.Clone(c)
			b[i*segCipher+at] ^= 1
			cases[fmt.Sprintf("byte %d of segment %d flipped", at, i)] = b
		}
	}
	for name, b := range cases {
		k := key
		if b == nil {
			b, k = c, NewDriveKey()
		}
		if err := readAllErr(t, k, b); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: got %v, want ErrDamaged", name, err)
		}
	}

	// An empty file authenticates too.
	empty := seal(t, key, nil)
	if err := readAllErr(t, key, empty); err != nil {
		t.Fatal(err)
	}
	if err := readAllErr(t, key, randBytes(len(empty))); !errors.Is(err, ErrDamaged) {
		t.Fatalf("forged empty file: %v", err)
	}
	if err := readAllErr(t, NewDriveKey(), empty); !errors.Is(err, ErrDamaged) {
		t.Fatalf("empty file, wrong key: %v", err)
	}

	// A seek straight into the middle still uses the file's own key.
	o, _ := open(t, NewDriveKey(), c)
	o.Seek(2*segPlain, io.SeekStart)
	if _, err := o.Read(make([]byte, 10)); !errors.Is(err, ErrDamaged) {
		t.Fatalf("mid-file read with the wrong key: %v", err)
	}

	// Nor can a mid-file read be pointed at another file's key.
	o, _ = open(t, key, cat(other[:segCipher], seg(1), seg(2), seg(3)))
	o.Seek(2*segPlain, io.SeekStart)
	if _, err := o.Read(make([]byte, 10)); !errors.Is(err, ErrDamaged) {
		t.Fatalf("mid-file read under another file's first nonce: %v", err)
	}

	// A file of whole segments cut on a segment boundary still has a valid
	// size, so only the final flag gives it away.
	whole := seal(t, key, randBytes(4*segPlain))
	if err := readAllErr(t, key, whole[:3*segCipher]); !errors.Is(err, ErrDamaged) {
		t.Fatalf("whole-segment file cut on a boundary: %v", err)
	}

	// A plain file left in an encrypted bucket is refused, never served.
	for _, n := range []int{segOverhead + 10, segCipher, 2*segCipher + 1000} {
		if err := readAllErr(t, key, randBytes(n)); !errors.Is(err, ErrDamaged) {
			t.Fatalf("plain %d-byte file: %v", n, err)
		}
	}
}

func TestRecoveryKey(t *testing.T) {
	key := NewDriveKey()
	s := FormatRecoveryKey(key)
	if len(s) != 56+13 || strings.Count(s, "-") != 13 {
		t.Fatalf("format: %q", s)
	}
	sloppy := strings.ToLower(strings.NewReplacer("-", " ", "O", "0", "I", "1", "B", "8").Replace(s))
	for _, in := range []string{s, strings.ReplaceAll(s, "-", ""), " " + sloppy + "\n"} {
		got, err := ParseRecoveryKey(in)
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("parse %q: %v", in, err)
		}
	}
	typo := []byte(s)
	if typo[5] == 'A' {
		typo[5] = 'C'
	} else {
		typo[5] = 'A'
	}
	for _, in := range []string{string(typo), s[:len(s)-1], s + "A", "", "not a key"} {
		if _, err := ParseRecoveryKey(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
	if KeyCheck(key) != KeyCheck(bytes.Clone(key)) || KeyCheck(key) == KeyCheck(NewDriveKey()) {
		t.Fatal("key check")
	}
}
