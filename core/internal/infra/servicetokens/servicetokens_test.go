package servicetokens_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/infra/servicetokens"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestIssueAll_WritesVerifiableTokenPerService(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "tokens")
	iss := &servicetokens.Issuer{Tokens: runtoken.NewIssuer(ring, time.Now), Dir: dir, TTL: time.Hour}

	require.NoError(t, iss.IssueAll())

	v := runtoken.NewRingVerifier(ring, time.Now)
	for _, s := range servicetokens.Services {
		path := filepath.Join(dir, s.Name+".token")
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		tok, err := runtoken.FileSource(path)(t.Context())
		require.NoError(t, err)
		c, err := v.Verify(t.Context(), tok, s.Audience[0])
		require.NoError(t, err)
		assert.Equal(t, runtoken.KindService, c.Kind)
		assert.Equal(t, "service:"+s.Name, c.Subject)
		assert.ElementsMatch(t, s.Capabilities, c.Capabilities)
	}
}
