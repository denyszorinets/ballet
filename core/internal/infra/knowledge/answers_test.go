package knowledge_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/report"
	"github.com/denyszorinets/ballet/core/internal/infra/knowledge"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

func TestReader_SearchAndRecordAnswer(t *testing.T) {
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), time.Now)
	require.NoError(t, err)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), time.Now)
	var created map[string]any
	var author string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := verifier.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "knowledge")
		require.NoError(t, err)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/customers/acme/knowledge/search":
			assert.Equal(t, []string{runtoken.CapKnowledgeRead}, c.Capabilities)
			assert.Equal(t, "WEB", r.URL.Query().Get("project"))
			_, _ = w.Write([]byte(`{"items":[{"entry":{"id":"k1","kind":"decision","title":"Use OIDC","body":"b1"},"score":1}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers/acme/knowledge/entries":
			assert.Equal(t, []string{runtoken.CapKnowledgeWrite}, c.Capabilities)
			author = c.ActingFor
			require.NoError(t, json.NewDecoder(r.Body).Decode(&created))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"k9"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	r := &knowledge.Reader{URL: u, Tokens: runtoken.NewIssuer(ring, time.Now)}

	found, err := r.Search(t.Context(), "acme", "WEB", "login", 5)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "k1", found[0].ID)

	q := report.Question{Text: "Which provider?", Context: "Keycloak or Okta", Answer: "Keycloak (see k1)."}
	require.NoError(t, r.RecordAnswer(t.Context(), "acme", "WEB", "WEB-3", "user-bob", q))
	assert.Equal(t, "user-bob", author)
	assert.Equal(t, "decision", created["kind"])
	assert.Equal(t, "Q: Which provider?", created["title"])
	assert.Equal(t, []any{"WEB-3"}, created["items"])
	assert.Contains(t, created["body"], "Keycloak (see k1).")
}
