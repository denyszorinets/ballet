package realtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/transport/realtime"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
	"github.com/denyszorinets/ballet/kit/rpc"
)

func TestRealtime_AuthenticatesWithOIDCAndAnswersPing(t *testing.T) {
	iss := oidctest.NewIssuer(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: "ballet"})
	require.NoError(t, err)
	mux := http.NewServeMux()
	realtime.Register(mux, realtime.Deps{Verifier: v, Now: time.Now})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + realtime.Path

	tok := iss.Token(t, "user-1", "ballet", nil)
	conn, res, err := rpc.Dial(t.Context(), url, rpc.DialOptions{Token: func(context.Context) (string, error) { return tok, nil }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	assert.Equal(t, "user-1", res.Subject)
	assert.Positive(t, res.ExpiresAt, "OIDC exp is reported so the client refreshes in time")

	var pong realtime.PingResult
	require.NoError(t, conn.Call(t.Context(), "system.ping", nil, &pong))
	assert.Equal(t, "user-1", pong.Subject)

	_, _, err = rpc.Dial(t.Context(), url, rpc.DialOptions{Token: func(context.Context) (string, error) { return "forged", nil }})
	assert.True(t, rpc.IsCode(err, rpc.CodeUnauthenticated))
}
