package local_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/local"
)

func TestLocal_EveryCallerIsTheLocalUser(t *testing.T) {
	var got auth.Identity
	h := local.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.FromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, local.Subject, got.Subject)
	assert.Equal(t, auth.KindHuman, got.Kind)

	id, err := local.Authenticator{}.Verify(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, local.Subject, id.Subject)
}
