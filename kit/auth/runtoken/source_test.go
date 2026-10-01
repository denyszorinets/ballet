package runtoken_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestFileSource_ReadsAndPicksUpReissuedTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	require.NoError(t, os.WriteFile(path, []byte("first\n"), 0o600))
	src := runtoken.FileSource(path)

	tok, err := src(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "first", tok, "trailing newline trimmed")

	require.NoError(t, os.WriteFile(path, []byte("second"), 0o600))
	future := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(path, future, future))
	tok, err = src(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "second", tok)
}

func TestFileSource_ErrorsWhenMissingOrEmpty(t *testing.T) {
	dir := t.TempDir()
	_, err := runtoken.FileSource(filepath.Join(dir, "absent"))(t.Context())
	assert.Error(t, err)

	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	_, err = runtoken.FileSource(empty)(t.Context())
	assert.Error(t, err)
}

func TestRingVerifier_FollowsKeyRotation(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	issuer := runtoken.NewIssuer(ring, clock.Now)
	v := runtoken.NewRingVerifier(ring, clock.Now)
	svc := runtoken.Claims{Kind: runtoken.KindService, Subject: "service:gateway", Audience: []string{"core"},
		Capabilities: []string{runtoken.CapCredentialsRead}}

	require.NoError(t, ring.Rotate(time.Hour))
	raw, err := issuer.Issue(svc, time.Hour)
	require.NoError(t, err)

	got, err := v.Verify(t.Context(), raw, "core")
	require.NoError(t, err)
	assert.True(t, got.Can(runtoken.CapCredentialsRead))
}
