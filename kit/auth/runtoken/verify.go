package runtoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// leeway tolerates small clock differences between Core and verifiers.
const leeway = 30 * time.Second

// minRefreshInterval limits JWKS refetches caused by repeated tokens naming
// the same unknown key (e.g. forged tokens). A newly seen unknown key always
// triggers one refetch, so rotations are picked up immediately.
const minRefreshInterval = 5 * time.Second

// Verifier validates run tokens.
type Verifier struct {
	now     func() time.Time
	refresh func(ctx context.Context) (jose.JSONWebKeySet, error) // nil for static keys

	mu       sync.Mutex
	keys     jose.JSONWebKeySet
	fetched  bool
	lastMiss struct {
		kid string
		at  time.Time
	}
}

// NewStaticVerifier verifies against a fixed key set (Core verifying its own
// tokens, tests).
func NewStaticVerifier(keys jose.JSONWebKeySet, now func() time.Time) *Verifier {
	return &Verifier{now: now, keys: keys, fetched: true}
}

// NewRemoteVerifier verifies against the JWKS at url (Core's
// /.well-known/jwks.json). Keys are fetched lazily, cached, and refetched
// when a token names an unknown key.
func NewRemoteVerifier(url string, client *http.Client, now func() time.Time) *Verifier {
	return &Verifier{now: now, refresh: func(ctx context.Context) (jose.JSONWebKeySet, error) {
		return fetchJWKS(ctx, client, url)
	}}
}

// Verify checks signature, issuer, audience and expiry of raw and returns
// its claims.
func (v *Verifier) Verify(ctx context.Context, raw, audience string) (Claims, error) {
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return Claims{}, fmt.Errorf("parse run token: %w", err)
	}
	if len(tok.Headers) != 1 {
		return Claims{}, errors.New("parse run token: expected one signature")
	}
	key, err := v.key(ctx, tok.Headers[0].KeyID)
	if err != nil {
		return Claims{}, err
	}

	var std jwt.Claims
	var wire wireClaims
	if err := tok.Claims(key.Key, &std, &wire); err != nil {
		return Claims{}, fmt.Errorf("verify run token: %w", err)
	}
	if err := std.ValidateWithLeeway(jwt.Expected{
		Issuer:      Issuer,
		AnyAudience: jwt.Audience{audience},
		Time:        v.now(),
	}, leeway); err != nil {
		return Claims{}, fmt.Errorf("verify run token: %w", err)
	}

	c := Claims{
		ID: std.ID, Kind: wire.Kind, Subject: std.Subject, Audience: std.Audience,
		Organization: wire.Organization, Project: wire.Project, Ticket: wire.Ticket,
		Session: wire.Session, Capabilities: wire.Capabilities,
	}
	if std.Expiry != nil {
		c.Expiry = std.Expiry.Time()
	}
	if wire.Actor != nil {
		c.ActingFor = wire.Actor.Subject
	}
	return c, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (jose.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.fetched {
		if err := v.fetchLocked(ctx); err != nil {
			return jose.JSONWebKey{}, err
		}
	}
	if keys := v.keys.Key(kid); len(keys) > 0 {
		return keys[0], nil
	}
	if v.refresh == nil {
		return jose.JSONWebKey{}, fmt.Errorf("verify run token: unknown signing key %q", kid)
	}

	now := v.now()
	if v.lastMiss.kid == kid && now.Sub(v.lastMiss.at) < minRefreshInterval {
		return jose.JSONWebKey{}, fmt.Errorf("verify run token: unknown signing key %q", kid)
	}
	v.lastMiss.kid, v.lastMiss.at = kid, now
	if err := v.fetchLocked(ctx); err != nil {
		return jose.JSONWebKey{}, err
	}
	if keys := v.keys.Key(kid); len(keys) > 0 {
		return keys[0], nil
	}
	return jose.JSONWebKey{}, fmt.Errorf("verify run token: unknown signing key %q", kid)
}

func (v *Verifier) fetchLocked(ctx context.Context) error {
	keys, err := v.refresh(ctx)
	if err != nil {
		return err
	}
	v.keys, v.fetched = keys, true
	return nil
}

func fetchJWKS(ctx context.Context, client *http.Client, url string) (jose.JSONWebKeySet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch run token keys: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch run token keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch run token keys: %s", resp.Status)
	}
	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("decode run token keys: %w", err)
	}
	return set, nil
}

// JWKSHandler serves the ring's public keys as a JWKS document.
func JWKSHandler(ring *KeyRing) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=60")
		_ = json.NewEncoder(w).Encode(ring.PublicKeys())
	})
}
