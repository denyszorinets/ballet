package forge_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/forge"
)

func TestParseRepo(t *testing.T) {
	for in, want := range map[string][3]string{
		"https://github.com/acme/web.git":    {"github.com", "acme", "web"},
		"https://github.com/acme/web":        {"github.com", "acme", "web"},
		"git@github.com:acme/web.git":        {"github.com", "acme", "web"},
		"ssh://git@ghe.example.com/acme/a.b": {"ghe.example.com", "acme", "a.b"},
	} {
		h, o, n, err := forge.ParseRepo(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, [3]string{h, o, n}, in)
	}
	_, _, _, err := forge.ParseRepo("file:///srv/repo.git")
	assert.Error(t, err)
	_, _, _, err = forge.ParseRepo("https://gitlab.com/group/sub/repo")
	assert.Error(t, err, "nested groups are not owner/name")
}
