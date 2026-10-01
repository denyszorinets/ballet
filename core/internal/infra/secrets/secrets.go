// Package secrets encrypts secrets at rest with AES-256-GCM using a key
// file that is generated on first use (mode 0600).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// prefix versions the ciphertext format so keys can be rotated later.
const prefix = "v1:"

// Box seals and opens secrets.
type Box struct {
	aead cipher.AEAD
}

// LoadKey reads the base64 key at path, creating a random 256-bit key if
// the file does not exist.
func LoadKey(path string) (*Box, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate secrets key: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create secrets key directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
			return nil, fmt.Errorf("write secrets key: %w", err)
		}
		return New(key)
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("decode secrets key %s: %w", path, err)
	}
	return New(key)
}

// New returns a box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secrets key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; aad binds the ciphertext to its owner (e.g. the
// credential ID) so it cannot be copied to another row.
func (b *Box) Seal(plaintext, aad string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seal secret: %w", err)
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return prefix + base64.StdEncoding.EncodeToString(ct), nil
}

// Open decrypts a value produced by Seal with the same aad.
func (b *Box) Open(sealed, aad string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return "", errors.New("open secret: unknown format")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(data) < b.aead.NonceSize() {
		return "", errors.New("open secret: corrupt value")
	}
	n := b.aead.NonceSize()
	pt, err := b.aead.Open(nil, data[:n], data[n:], []byte(aad))
	if err != nil {
		return "", errors.New("open secret: authentication failed")
	}
	return string(pt), nil
}
