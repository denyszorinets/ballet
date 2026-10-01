package embed_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/embed"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHash_DeterministicNormalisedAndLexicallySensitive(t *testing.T) {
	vs, err := embed.Hash{}.Embed(t.Context(), []string{
		"Export invoices as CSV", "export INVOICES as csv!", "CSV export of invoices", "OIDC login with Keycloak", "",
	})
	require.NoError(t, err)

	for _, v := range vs {
		assert.Len(t, v, 256)
		assert.InDelta(t, 1, cosine(v, v), 1e-5, "unit length")
	}
	assert.InDelta(t, 1, cosine(vs[0], vs[1]), 1e-5, "case and punctuation insensitive")
	assert.Greater(t, cosine(vs[0], vs[2]), cosine(vs[0], vs[3]), "shared words are closer")
	again, _ := embed.Hash{}.Embed(t.Context(), []string{"Export invoices as CSV"})
	assert.Equal(t, vs[0], again[0])
	assert.Equal(t, embed.HashModel, embed.Hash{}.Model())
}

func TestWords(t *testing.T) {
	assert.Equal(t, []string{"export", "invoices", "v2", "csv"}, embed.Words("Export invoices (v2) → CSV"))
}
