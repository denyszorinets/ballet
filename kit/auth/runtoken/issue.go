package runtoken

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// wireClaims is the JSON representation of the Ballet-specific claims.
type wireClaims struct {
	Kind         Kind     `json:"kind"`
	Organization string   `json:"org,omitempty"`
	Project      string   `json:"proj,omitempty"`
	Ticket       string   `json:"tkt,omitempty"`
	Session      string   `json:"sess,omitempty"`
	Actor        *actor   `json:"act,omitempty"` // RFC 8693 actor claim
	Capabilities []string `json:"caps,omitempty"`
}

type actor struct {
	Subject string `json:"sub"`
}

// TokenIssuer signs run tokens with the ring's active key.
type TokenIssuer struct {
	ring *KeyRing
	now  func() time.Time
}

// NewIssuer returns an issuer signing with ring.
func NewIssuer(ring *KeyRing, now func() time.Time) *TokenIssuer {
	return &TokenIssuer{ring: ring, now: now}
}

// Issue validates c and returns a signed token valid for ttl.
func (i *TokenIssuer) Issue(c Claims, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", errors.New("issue run token: ttl must be positive")
	}
	if err := c.Validate(); err != nil {
		return "", fmt.Errorf("issue run token: %w", err)
	}
	kid, key := i.ring.active()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid),
	)
	if err != nil {
		return "", fmt.Errorf("issue run token: %w", err)
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("issue run token: %w", err)
	}
	now := i.now()
	std := jwt.Claims{
		ID:       hex.EncodeToString(jti),
		Issuer:   Issuer,
		Subject:  c.Subject,
		Audience: jwt.Audience(c.Audience),
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(ttl)),
	}
	wire := wireClaims{
		Kind: c.Kind, Organization: c.Organization, Project: c.Project, Ticket: c.Ticket,
		Session: c.Session, Capabilities: c.Capabilities,
	}
	if c.ActingFor != "" {
		wire.Actor = &actor{Subject: c.ActingFor}
	}
	raw, err := jwt.Signed(signer).Claims(std).Claims(wire).Serialize()
	if err != nil {
		return "", fmt.Errorf("issue run token: %w", err)
	}
	return raw, nil
}
