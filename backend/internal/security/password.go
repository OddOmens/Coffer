// Package security holds Coffer's cryptographic primitives: password hashing,
// random tokens, sealed secrets, TOTP and rate limiting.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. 64 MiB / 2 passes / 2 lanes is above the OWASP minimum
// while staying around 50-100 ms on a small server.
const (
	argonMemory  = 64 * 1024
	argonTime    = 2
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots bounds concurrent hashing so a login flood cannot exhaust memory.
var hashSlots = make(chan struct{}, 4)

var ErrBadHash = errors.New("security: malformed password hash")

const MinPasswordLength = 10

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSlots
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against an encoded hash. needsRehash is true
// when the hash was made with weaker parameters than the current ones.
func VerifyPassword(password, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, ErrBadHash
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, false, ErrBadHash
	}
	if mem == 0 || mem > 1<<21 || t == 0 || t > 16 || p == 0 {
		return false, false, ErrBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false, ErrBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, false, ErrBadHash
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	<-hashSlots
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = mem < argonMemory || t < argonTime || p < argonThreads
	return ok, needsRehash, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// DummyVerify burns the same time as a real check, so a login for an unknown
// user is not distinguishable by timing.
func DummyVerify(password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword(RandomToken(16)) })
	_, _, _ = VerifyPassword(password, dummyHash)
}
