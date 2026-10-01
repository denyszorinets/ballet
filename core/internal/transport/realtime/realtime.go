// Package realtime serves Core's stateful API (ADR-0018): authenticated
// JSON-RPC over WebSocket at /rpc.
package realtime

import (
	"context"
	"net/http"
	"time"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/rpc"
)

// Path is where the realtime API is served.
const Path = "/rpc"

// Deps are the collaborators of the realtime API.
type Deps struct {
	Verifier *oidc.Verifier
	Options  rpc.Options
	Now      func() time.Time
}

// Register mounts the realtime API on mux.
func Register(mux *http.ServeMux, d Deps) {
	opts := d.Options
	opts.Handler = handler(d)
	mux.Handle("GET "+Path, rpc.NewServer(rpc.ServerOptions{
		Options:      opts,
		Authenticate: authenticator(d.Verifier),
	}))
}

// authenticator accepts OIDC access tokens; the connection must refresh
// before the token's exp.
func authenticator(v *oidc.Verifier) rpc.Authenticator {
	return func(ctx context.Context, token string) (auth.Identity, time.Time, error) {
		id, err := v.Verify(ctx, token)
		if err != nil {
			return auth.Identity{}, time.Time{}, err
		}
		var expiry time.Time
		if exp, ok := id.Claims["exp"].(float64); ok {
			expiry = time.Unix(int64(exp), 0)
		}
		return id, expiry, nil
	}
}

// PingResult is the result of system.ping.
type PingResult struct {
	Time    int64  `json:"time"` // server clock, Unix milliseconds
	Subject string `json:"subject"`
}

func handler(d Deps) rpc.Handler {
	return func(ctx context.Context, req *rpc.Request) (any, error) {
		switch req.Method {
		case "system.ping":
			id, _ := auth.FromContext(ctx)
			return PingResult{Time: d.Now().UnixMilli(), Subject: id.Subject}, nil
		}
		return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
	}
}
