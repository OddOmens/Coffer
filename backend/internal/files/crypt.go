package files

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strings"
)

// Encrypted S3 drives store every file as a run of sealed segments, so the
// bucket only ever holds ciphertext while Coffer serves plain files (ranges
// included) to every signed-in device:
//
//	segment = nonce (12 bytes) || AES-256-GCM(fileKey, nonce, plain, index || final)
//
// Each segment holds segPlain bytes of the file except the last, which may
// be shorter; an empty file is one empty segment. fileKey comes from the
// drive key and the first segment's nonce, so every file version has its own
// key. Every nonce is random, never a counter: a retried upload chunk may
// carry different bytes, and GCM must never seal two under one nonce. With a
// key per file, its random nonces stay far below GCM's limit. The segment index and final flag are authenticated, so
// segments cannot be reordered, dropped, cut short or moved between files
// unnoticed. There is no header: a 16 MiB upload chunk is exactly 256
// segments, which keeps multipart parts equal-sized as R2 requires.
//
// What the bucket still shows: object names, folder layout, approximate
// sizes and times. Backups to an encrypted drive hide names too.
const (
	DriveKeySize = 32
	segPlain     = 64 << 10
	nonceSize    = 12 // GCM's standard nonce
	tagSize      = 16
	segOverhead  = nonceSize + tagSize
	segCipher    = segPlain + segOverhead
)

// ErrDamaged reports an encrypted file that fails to authenticate: changed in
// the bucket, cut short, or sealed with another key.
var ErrDamaged = errors.New("this file is damaged or was not encrypted with this drive's key")

// NewDriveKey returns a random drive key.
func NewDriveKey() []byte {
	k := make([]byte, DriveKeySize)
	rand.Read(k)
	return k
}

func newNonce() []byte {
	n := make([]byte, nonceSize)
	rand.Read(n)
	return n
}

func fileAEAD(driveKey, nonce0 []byte) (cipher.AEAD, error) {
	k, err := hkdf.Key(sha256.New, driveKey, nonce0, "coffer/file/aes256gcm/v2", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func segAD(idx int64, final bool) []byte {
	ad := make([]byte, 9)
	binary.BigEndian.PutUint64(ad, uint64(idx))
	if final {
		ad[8] = 1
	}
	return ad
}

// cipherSize is the stored size of n plain bytes starting on a segment
// boundary: a whole file, or one upload chunk.
func cipherSize(n int64) int64 {
	segs := max((n+segPlain-1)/segPlain, 1)
	return n + segs*segOverhead
}

// plainSize inverts cipherSize for a whole file. ok is false for a size
// that no sealed file can have.
func plainSize(c int64) (int64, bool) {
	full, rem := c/segCipher, c%segCipher
	switch {
	case rem > segOverhead:
		return full*segPlain + rem - segOverhead, true
	case rem == 0 && full > 0:
		return full * segPlain, true
	case rem == segOverhead && full == 0:
		return 0, true
	}
	return 0, false
}

// sealer encrypts n plain bytes read from src into the segments that start
// at plain offset off of a file. It reads exactly n bytes: a short src is an
// error, never a short file.
type sealer struct {
	src    io.Reader
	aead   cipher.AEAD
	nonce0 []byte
	idx    int64 // next segment
	left   int64 // plain bytes still to read
	final  bool  // the last segment here ends the file
	begun  bool
	buf    []byte
	out    []byte
	off    int
	err    error
}

// newSealer starts a sealer. nonce0 is the file's first-segment nonce; nil
// makes a new one, for a whole file written at once.
func newSealer(driveKey, nonce0 []byte, off, n int64, final bool, src io.Reader) (*sealer, error) {
	if off%segPlain != 0 || n < 0 || (n == 0 && (off != 0 || !final)) {
		return nil, errors.New("files: bad sealed range")
	}
	if nonce0 == nil {
		if off != 0 {
			return nil, errors.New("files: missing file nonce")
		}
		nonce0 = newNonce()
	}
	aead, err := fileAEAD(driveKey, nonce0)
	if err != nil {
		return nil, err
	}
	return &sealer{src: src, aead: aead, nonce0: nonce0, idx: off / segPlain, left: n, final: final,
		buf: make([]byte, segPlain)}, nil
}

func (s *sealer) Read(p []byte) (int, error) {
	for s.off == len(s.out) {
		if s.err != nil {
			return 0, s.err
		}
		s.err = s.next()
	}
	n := copy(p, s.out[s.off:])
	s.off += n
	return n, nil
}

func (s *sealer) next() error {
	if s.left == 0 && s.begun {
		return io.EOF
	}
	s.begun = true
	plain := s.buf[:min(s.left, segPlain)]
	if _, err := io.ReadFull(s.src, plain); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	s.left -= int64(len(plain))
	nonce := s.nonce0
	if s.idx != 0 {
		nonce = newNonce()
	}
	s.out = s.aead.Seal(append(s.out[:0], nonce...), nonce, plain, segAD(s.idx, s.final && s.left == 0))
	s.off = 0
	s.idx++
	return nil
}

// opener decrypts a sealed file a segment at a time, so a seek (video
// scrubbing, a resumed download) only fetches the segments it needs. No
// byte of a segment is returned before the whole segment authenticates.
type opener struct {
	src io.ReadSeekCloser
	// head fetches the file's first nonce on its own, so a read that starts
	// mid-file doesn't open the whole object (nil: read it from src).
	head  func() ([]byte, error)
	key   []byte
	aead  cipher.AEAD
	csize int64
	size  int64
	pos   int64 // plain read position
	at    int64 // src position
	seg   int64 // segment held in plain, or -1
	buf   []byte
	plain []byte
}

func newOpener(driveKey []byte, src io.ReadSeekCloser, csize int64) (*opener, error) {
	size, ok := plainSize(csize)
	if !ok {
		return nil, ErrDamaged
	}
	return &opener{src: src, key: driveKey, csize: csize, size: size, seg: -1,
		buf: make([]byte, segCipher), plain: make([]byte, 0, segPlain)}, nil
}

func (o *opener) Read(p []byte) (int, error) {
	if o.pos >= o.size {
		// An empty file still has to authenticate.
		if o.size == 0 && o.seg < 0 {
			if err := o.load(0); err != nil {
				return 0, err
			}
		}
		return 0, io.EOF
	}
	idx := o.pos / segPlain
	if idx != o.seg {
		if err := o.load(idx); err != nil {
			return 0, err
		}
	}
	n := copy(p, o.plain[o.pos-idx*segPlain:])
	o.pos += int64(n)
	return n, nil
}

func (o *opener) load(idx int64) error {
	o.seg = -1
	if o.aead == nil && idx != 0 {
		nonce0 := o.buf[:nonceSize]
		var err error
		if o.head != nil {
			nonce0, err = o.head()
		} else {
			err = o.readAt(0, nonce0)
		}
		if err != nil {
			return err
		}
		if err := o.keyFrom(nonce0); err != nil {
			return err
		}
	}
	off := idx * segCipher
	c := o.buf[:min(segCipher, o.csize-off)]
	if err := o.readAt(off, c); err != nil {
		return err
	}
	nonce := c[:nonceSize]
	if o.aead == nil {
		if err := o.keyFrom(nonce); err != nil {
			return err
		}
	}
	last := (o.csize - 1) / segCipher
	plain, err := o.aead.Open(o.plain[:0], nonce, c[len(nonce):], segAD(idx, idx == last))
	if err != nil {
		return ErrDamaged
	}
	o.plain, o.seg = plain, idx
	return nil
}

func (o *opener) keyFrom(nonce0 []byte) error {
	aead, err := fileAEAD(o.key, nonce0)
	o.aead = aead
	return err
}

func (o *opener) readAt(off int64, b []byte) error {
	if o.at != off {
		if _, err := o.src.Seek(off, io.SeekStart); err != nil {
			return err
		}
		o.at = off
	}
	n, err := io.ReadFull(o.src, b)
	o.at += int64(n)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// Shorter than its listed size: changed underneath us.
		return ErrDamaged
	}
	return err
}

func (o *opener) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += o.pos
	case io.SeekEnd:
		offset += o.size
	default:
		return 0, errors.New("files: invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("files: negative position")
	}
	o.pos = offset
	return offset, nil
}

func (o *opener) Close() error { return o.src.Close() }

// KeyCheck identifies a drive key without revealing it, so a bucket can say
// which key its files need.
func KeyCheck(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("coffer/key-check/v1"))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:16])
}

func nameKey(driveKey []byte) []byte {
	k, err := hkdf.Key(sha256.New, driveKey, nil, "coffer/names/v1", 32)
	if err != nil {
		panic("files: hkdf: " + err.Error())
	}
	return k
}

// Recovery keys are a drive key written out for people to keep: base32 in
// groups of four with a 3-byte checksum, 56 characters in all.

var recoveryEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

var errRecoveryKey = errors.New("that recovery key is not valid: check it for typos")

func FormatRecoveryKey(key []byte) string {
	sum := sha256.Sum256(key)
	s := recoveryEnc.EncodeToString(append(slices.Clone(key), sum[:3]...))
	var b strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(s[i:min(i+4, len(s))])
	}
	return b.String()
}

// ParseRecoveryKey reads a recovery key, forgiving case, spacing, dashes and
// digits typed for the letters they look like.
func ParseRecoveryKey(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\t', '\r', '\n':
			return -1
		case '0':
			return 'O'
		case '1':
			return 'I'
		case '8':
			return 'B'
		}
		return r
	}, strings.ToUpper(s))
	// Check the length first: the decoder ignores a stray last character.
	if len(s) != recoveryEnc.EncodedLen(DriveKeySize+3) {
		return nil, errRecoveryKey
	}
	b, err := recoveryEnc.DecodeString(s)
	if err != nil || len(b) != DriveKeySize+3 {
		return nil, errRecoveryKey
	}
	key := b[:DriveKeySize]
	sum := sha256.Sum256(key)
	if subtle.ConstantTimeCompare(sum[:3], b[DriveKeySize:]) != 1 {
		return nil, errRecoveryKey
	}
	return key, nil
}
