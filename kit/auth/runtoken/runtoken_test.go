package runtoken_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/runtoken"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newRing(t *testing.T, clock *fakeClock) *runtoken.KeyRing {
	t.Helper()
	ring, err := runtoken.LoadKeyRing(filepath.Join(t.TempDir(), "keys.json"), clock.Now)
	require.NoError(t, err)
	return ring
}

func runClaims() runtoken.Claims {
	return runtoken.Claims{
		Kind:         runtoken.KindRun,
		Subject:      "run:r-42",
		Audience:     []string{"knowledge", "gateway"},
		Customer:     "acme",
		Project:      "ACME",
		Ticket:       "ACME-7",
		Capabilities: []string{runtoken.CapKnowledgeRead, runtoken.CapLLMInvoke},
	}
}

func TestIssueAndVerify_RoundTripsClaims(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	issuer := runtoken.NewIssuer(ring, clock.Now)

	raw, err := issuer.Issue(runClaims(), time.Hour)
	require.NoError(t, err)

	got, err := runtoken.NewStaticVerifier(ring.PublicKeys(), clock.Now).Verify(t.Context(), raw, "knowledge")
	require.NoError(t, err)
	assert.Equal(t, runtoken.KindRun, got.Kind)
	assert.Equal(t, "run:r-42", got.Subject)
	assert.Equal(t, "acme", got.Customer)
	assert.Equal(t, "ACME", got.Project)
	assert.Equal(t, "ACME-7", got.Ticket)
	assert.True(t, got.Can(runtoken.CapLLMInvoke))
	assert.False(t, got.Can(runtoken.CapKnowledgeWrite))
	assert.WithinDuration(t, clock.now.Add(time.Hour), got.Expiry, time.Second)
	assert.NotEmpty(t, got.ID)
}

func TestIssue_PlannerTokenRecordsActingHuman(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	c := runtoken.Claims{
		Kind: runtoken.KindPlanner, Subject: "planner:s-1", Audience: []string{"knowledge"},
		Customer: "acme", Project: "ACME", Session: "s-1", ActingFor: "user-bob",
	}

	raw, err := runtoken.NewIssuer(ring, clock.Now).Issue(c, time.Hour)
	require.NoError(t, err)
	got, err := runtoken.NewStaticVerifier(ring.PublicKeys(), clock.Now).Verify(t.Context(), raw, "knowledge")

	require.NoError(t, err)
	assert.Equal(t, "user-bob", got.ActingFor)
	assert.Equal(t, "s-1", got.Session)
}

func TestIssue_RejectsInvalidClaims(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	issuer := runtoken.NewIssuer(newRing(t, clock), clock.Now)

	tests := map[string]func(*runtoken.Claims){
		"missing kind":     func(c *runtoken.Claims) { c.Kind = "" },
		"missing subject":  func(c *runtoken.Claims) { c.Subject = "" },
		"missing audience": func(c *runtoken.Claims) { c.Audience = nil },
		"run without ticket": func(c *runtoken.Claims) {
			c.Ticket = ""
		},
		"unknown capability": func(c *runtoken.Claims) { c.Capabilities = []string{"root"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := runClaims()
			mutate(&c)
			_, err := issuer.Issue(c, time.Hour)
			assert.Error(t, err)
		})
	}

	_, err := issuer.Issue(runClaims(), 0)
	assert.Error(t, err, "non-positive TTL")
}

func TestVerify_RejectsBadTokens(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	otherRing := newRing(t, clock)
	issuer := runtoken.NewIssuer(ring, clock.Now)
	verifier := runtoken.NewStaticVerifier(ring.PublicKeys(), clock.Now)

	valid, err := issuer.Issue(runClaims(), time.Minute)
	require.NoError(t, err)
	foreign, err := runtoken.NewIssuer(otherRing, clock.Now).Issue(runClaims(), time.Minute)
	require.NoError(t, err)

	t.Run("wrong audience", func(t *testing.T) {
		_, err := verifier.Verify(t.Context(), valid, "core")
		assert.Error(t, err)
	})
	t.Run("signed with unknown key", func(t *testing.T) {
		_, err := verifier.Verify(t.Context(), foreign, "knowledge")
		assert.Error(t, err)
	})
	t.Run("tampered", func(t *testing.T) {
		_, err := verifier.Verify(t.Context(), valid[:len(valid)-2]+"xx", "knowledge")
		assert.Error(t, err)
	})
	t.Run("expired", func(t *testing.T) {
		later := &fakeClock{now: clock.now.Add(2 * time.Minute)}
		_, err := runtoken.NewStaticVerifier(ring.PublicKeys(), later.Now).Verify(t.Context(), valid, "knowledge")
		assert.Error(t, err)
	})
}

func TestKeyRing_RotationKeepsOldTokensValidUntilRetired(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	path := filepath.Join(t.TempDir(), "keys.json")
	ring, err := runtoken.LoadKeyRing(path, clock.Now)
	require.NoError(t, err)
	issuer := runtoken.NewIssuer(ring, clock.Now)
	old, err := issuer.Issue(runClaims(), time.Hour)
	require.NoError(t, err)

	require.NoError(t, ring.Rotate(2*time.Hour))
	fresh, err := issuer.Issue(runClaims(), time.Hour)
	require.NoError(t, err)

	// Reloading from disk keeps both keys.
	reloaded, err := runtoken.LoadKeyRing(path, clock.Now)
	require.NoError(t, err)
	v := runtoken.NewStaticVerifier(reloaded.PublicKeys(), clock.Now)
	_, err = v.Verify(t.Context(), old, "knowledge")
	assert.NoError(t, err, "token signed with the previous key stays valid")
	_, err = v.Verify(t.Context(), fresh, "knowledge")
	assert.NoError(t, err)

	// After the retirement period the old key is dropped.
	clock.now = clock.now.Add(3 * time.Hour)
	require.NoError(t, ring.Rotate(2*time.Hour))
	assert.Len(t, ring.PublicKeys().Keys, 2, "keys: previous active (retiring) + new active")
	_, err = runtoken.NewStaticVerifier(ring.PublicKeys(), clock.Now).Verify(t.Context(), old, "knowledge")
	assert.Error(t, err)
}

func TestRemoteVerifier_FetchesJWKSAndRefreshesOnUnknownKey(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	issuer := runtoken.NewIssuer(ring, clock.Now)
	fetches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches++
		runtoken.JWKSHandler(ring).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	v := runtoken.NewRemoteVerifier(srv.URL, srv.Client(), clock.Now)

	first, err := issuer.Issue(runClaims(), time.Hour)
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), first, "knowledge")
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), first, "knowledge")
	require.NoError(t, err)
	assert.Equal(t, 1, fetches, "keys are cached")

	require.NoError(t, ring.Rotate(time.Hour))
	second, err := issuer.Issue(runClaims(), time.Hour)
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), second, "knowledge")
	require.NoError(t, err)
	assert.Equal(t, 2, fetches, "unknown key id triggers a refresh")
}

func TestMiddleware_RequiresTokenForAudienceAndSetsIdentity(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	raw, err := runtoken.NewIssuer(ring, clock.Now).Issue(runClaims(), time.Hour)
	require.NoError(t, err)
	v := runtoken.NewStaticVerifier(ring.PublicKeys(), clock.Now)

	var gotID auth.Identity
	var gotClaims runtoken.Claims
	h := runtoken.Middleware(v, "knowledge")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, _ = auth.FromContext(r.Context())
		gotClaims, _ = runtoken.ClaimsFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, auth.KindService, gotID.Kind)
	assert.Equal(t, "run:r-42", gotID.Subject)
	assert.Equal(t, "ACME-7", gotClaims.Ticket)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRemoteVerifier_ThrottlesRepeatedUnknownKey(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	ring := newRing(t, clock)
	foreignRing := newRing(t, clock)
	fetches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches++
		runtoken.JWKSHandler(ring).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	v := runtoken.NewRemoteVerifier(srv.URL, srv.Client(), clock.Now)
	forged, err := runtoken.NewIssuer(foreignRing, clock.Now).Issue(runClaims(), time.Hour)
	require.NoError(t, err)

	for range 5 {
		_, err := v.Verify(t.Context(), forged, "knowledge")
		assert.Error(t, err)
	}

	assert.Equal(t, 2, fetches, "initial fetch + one refetch for the unknown key, then throttled")
}
