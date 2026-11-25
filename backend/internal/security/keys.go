package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RandomToken returns n random bytes encoded as unpadded base64url.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the lookup hash stored in place of a bearer token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Keys holds the keys derived from the instance secret.
type Keys struct {
	aead cipher.AEAD
	mac  []byte
}

// LoadKeys derives keys from override (if set) or from <dataDir>/secret.key,
// creating that file on first start.
func LoadKeys(dataDir, override string) (*Keys, error) {
	master, err := masterKey(dataDir, override)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(derive(master, "coffer/enc/v1"))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keys{aead: aead, mac: derive(master, "coffer/mac/v1")}, nil
}

func masterKey(dataDir, override string) ([]byte, error) {
	if override != "" {
		for _, dec := range []func(string) ([]byte, error){
			hex.DecodeString, base64.StdEncoding.DecodeString, base64.RawURLEncoding.DecodeString,
		} {
			if k, err := dec(override); err == nil && len(k) >= 32 {
				return k, nil
			}
		}
		return nil, errors.New("COFFER_SECRET_KEY must be hex or base64 encoding at least 32 bytes")
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) < 32 {
			return nil, fmt.Errorf("%s is corrupt", path)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(k) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	return k, f.Close()
}

func derive(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// Encrypt seals plain with AES-256-GCM.
func (k *Keys) Encrypt(plain []byte) string {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(k.aead.Seal(nonce, nonce, plain, nil))
}

func (k *Keys) Decrypt(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) < k.aead.NonceSize() {
		return nil, errors.New("security: bad ciphertext")
	}
	n := k.aead.NonceSize()
	return k.aead.Open(nil, b[:n], b[n:], nil)
}

// Sign returns a MAC over parts.
func (k *Keys) Sign(parts ...string) string {
	m := hmac.New(sha256.New, k.mac)
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (k *Keys) Verify(sig string, parts ...string) bool {
	return subtle.ConstantTimeCompare([]byte(sig), []byte(k.Sign(parts...))) == 1
}
