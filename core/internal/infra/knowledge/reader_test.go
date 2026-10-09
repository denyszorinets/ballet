package knowledge_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/onboarding"
	"github.com/denyszorinets/ballet/core/internal/infra/knowledge"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestReader_LinkedThenSearched(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := verifier.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "knowledge")
		require.NoError(t, err)
		assert.Equal(t, "acme", c.Organization)
		assert.Equal(t, []string{runtoken.CapKnowledgeRead}, c.Capabilities, "read only")
		switch r.URL.Path {
		case "/v1/organizations/acme/knowledge/entries":
			assert.Equal(t, "WEB-3", r.URL.Query().Get("item"))
			_, _ = w.Write([]byte(`{"items":[{"id":"k1","kind":"decision","title":"Use OIDC","body":"b1"}]}`))
		case "/v1/organizations/acme/knowledge/search":
			assert.Equal(t, "Login", r.URL.Query().Get("q"))
			assert.Equal(t, "WEB", r.URL.Query().Get("project"))
			_, _ = w.Write([]byte(`{"items":[{"entry":{"id":"k1","kind":"decision","title":"Use OIDC","body":"b1"},"score":1},
				{"entry":{"id":"k2","kind":"note","title":"Sessions","body":"b2"},"score":0.5},
				{"entry":{"id":"k3","kind":"note","title":"Other","body":"b3"},"score":0.1}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	r := &knowledge.Reader{URL: u, Tokens: runtoken.NewIssuer(ring, time.Now)}

	got, err := r.ForTicket(t.Context(), "acme", "WEB", "WEB-3", "Login", 1)
	require.NoError(t, err)
	assert.Equal(t, []onboarding.Knowledge{
		{ID: "k1", Kind: "decision", Title: "Use OIDC", Body: "b1", Linked: true},
		{ID: "k2", Kind: "note", Title: "Sessions", Body: "b2"},
	}, got)
}
