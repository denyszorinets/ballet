package runtoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// KeyRing holds Core's token signing keys: one active key and keys being
// retired. It is persisted to a JSON file (mode 0600) so tokens survive
// restarts.
type KeyRing struct {
	mu   sync.RWMutex
	path string
	now  func() time.Time
	keys []ringKey // keys[len-1] is the active key
}

type ringKey struct {
	ID        string    `json:"id"`
	Seed      []byte    `json:"seed"` // Ed25519 private key seed
	CreatedAt time.Time `json:"created_at"`
	RetiresAt time.Time `json:"retires_at,omitzero"` // zero for the active key
}

// LoadKeyRing loads the ring from path, creating it with a fresh active key
// if the file does not exist.
func LoadKeyRing(path string, now func() time.Time) (*KeyRing, error) {
	r := &KeyRing{path: path, now: now}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := r.addActive(); err != nil {
			return nil, err
		}
		return r, r.save()
	case err != nil:
		return nil, fmt.Errorf("read token keys: %w", err)
	}
	if err := json.Unmarshal(data, &r.keys); err != nil {
		return nil, fmt.Errorf("parse token keys %s: %w", path, err)
	}
	if len(r.keys) == 0 {
		return nil, fmt.Errorf("token keys %s: no keys", path)
	}
	return r, nil
}

// Rotate makes a new key active. The previous active key keeps verifying
// tokens for retireAfter (set it to at least the longest token TTL); keys
// whose retirement time has passed are removed.
func (r *KeyRing) Rotate(retireAfter time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	kept := r.keys[:0]
	for _, k := range r.keys {
		if !k.RetiresAt.IsZero() && !now.Before(k.RetiresAt) {
			continue
		}
		if k.RetiresAt.IsZero() {
			k.RetiresAt = now.Add(retireAfter)
		}
		kept = append(kept, k)
	}
	r.keys = kept
	if err := r.addActive(); err != nil {
		return err
	}
	return r.save()
}

// PublicKeys returns the verification keys of all keys in the ring.
func (r *KeyRing) PublicKeys() jose.JSONWebKeySet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set := jose.JSONWebKeySet{}
	for _, k := range r.keys {
		priv := ed25519.NewKeyFromSeed(k.Seed)
		set.Keys = append(set.Keys, jose.JSONWebKey{
			Key: priv.Public(), KeyID: k.ID, Algorithm: string(jose.EdDSA), Use: "sig",
		})
	}
	return set
}

func (r *KeyRing) active() (id string, key ed25519.PrivateKey) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k := r.keys[len(r.keys)-1]
	return k.ID, ed25519.NewKeyFromSeed(k.Seed)
}

func (r *KeyRing) addActive() error {
	seed := make([]byte, ed25519.SeedSize)
	idBytes := make([]byte, 8)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("generate token key: %w", err)
	}
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate token key id: %w", err)
	}
	r.keys = append(r.keys, ringKey{ID: hex.EncodeToString(idBytes), Seed: seed, CreatedAt: r.now()})
	return nil
}

// save writes the ring atomically (temp file + rename).
func (r *KeyRing) save() error {
	data, err := json.MarshalIndent(r.keys, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token keys: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("create token key directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".keys-*")
	if err != nil {
		return fmt.Errorf("write token keys: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write token keys: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write token keys: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("write token keys: %w", err)
	}
	if err := os.Rename(tmp.Name(), r.path); err != nil {
		return fmt.Errorf("write token keys: %w", err)
	}
	return nil
}
