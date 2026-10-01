package secrets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/infra/secrets"
)

func TestBox_SealOpenRoundTripAndAADBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k", "secrets.key")
	box, err := secrets.LoadKey(path)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	sealed, err := box.Seal("sk-ant-secret", "cred-1")
	require.NoError(t, err)
	assert.NotContains(t, sealed, "sk-ant")

	got, err := box.Open(sealed, "cred-1")
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-secret", got)

	_, err = box.Open(sealed, "cred-2")
	assert.Error(t, err, "ciphertext bound to its owner")

	again, err := box.Seal("sk-ant-secret", "cred-1")
	require.NoError(t, err)
	assert.NotEqual(t, sealed, again, "random nonce")

	reloaded, err := secrets.LoadKey(path)
	require.NoError(t, err)
	got, err = reloaded.Open(sealed, "cred-1")
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-secret", got)

	other, err := secrets.LoadKey(filepath.Join(t.TempDir(), "other.key"))
	require.NoError(t, err)
	_, err = other.Open(sealed, "cred-1")
	assert.Error(t, err, "another key cannot open it")
	_, err = box.Open("garbage", "cred-1")
	assert.Error(t, err)
}
