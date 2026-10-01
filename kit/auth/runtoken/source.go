package runtoken

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// FileSource returns a token source reading a token file written by Core
// (service tokens). The file is re-read when it changes, so re-issued
// tokens are picked up without restart.
func FileSource(path string) func(context.Context) (string, error) {
	var mu sync.Mutex
	var token string
	var mod time.Time
	return func(context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("read service token: %w", err)
		}
		if token != "" && info.ModTime().Equal(mod) {
			return token, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read service token: %w", err)
		}
		t := strings.TrimSpace(string(data))
		if t == "" {
			return "", errors.New("read service token: file is empty")
		}
		token, mod = t, info.ModTime()
		return token, nil
	}
}

// NewRingVerifier verifies against the current keys of ring (Core
// verifying tokens it issued, across rotations).
func NewRingVerifier(ring *KeyRing, now func() time.Time) *Verifier {
	return &Verifier{now: now, refresh: func(context.Context) (jose.JSONWebKeySet, error) {
		return ring.PublicKeys(), nil
	}}
}
