package rpc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/denyszorinets/ballet/kit/auth"
)

// DialOptions configure a client connection.
type DialOptions struct {
	Options
	// Subprotocols to offer, preferred first (default: msgpack, json).
	Subprotocols []string
	// Token returns the access token for auth and auth.refresh.
	Token func(ctx context.Context) (string, error)
	// HTTPClient used for the upgrade request (optional).
	HTTPClient *http.Client
}

// refreshAt is the fraction of the token lifetime after which the client
// refreshes it.
const refreshAt = 0.8

// Dial connects to url (ws:// or wss://), authenticates and starts the
// connection. Tokens are refreshed automatically before they expire.
func Dial(ctx context.Context, url string, opts DialOptions) (*Conn, AuthResult, error) {
	if opts.Token == nil {
		return nil, AuthResult{}, fmt.Errorf("rpc: DialOptions.Token is required")
	}
	protos := opts.Subprotocols
	if len(protos) == 0 {
		protos = Subprotocols
	}
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{Subprotocols: protos, HTTPClient: opts.HTTPClient})
	if err != nil {
		return nil, AuthResult{}, fmt.Errorf("rpc: dial %s: %w", url, err)
	}
	c, err := codecFor(ws.Subprotocol())
	if err != nil {
		_ = ws.Close(websocket.StatusProtocolError, "no supported subprotocol")
		return nil, AuthResult{}, err
	}
	conn := newConn(ws, c, opts.Options)
	conn.start()

	res, err := authCall(ctx, conn, MethodAuth, opts.Token)
	if err != nil {
		conn.closeWith(websocket.StatusNormalClosure, "auth failed", err)
		return nil, AuthResult{}, err
	}
	go conn.refreshLoop(res, opts.Token)
	return conn, res, nil
}

func authCall(ctx context.Context, conn *Conn, method string, token func(context.Context) (string, error)) (AuthResult, error) {
	tok, err := token(ctx)
	if err != nil {
		return AuthResult{}, fmt.Errorf("rpc: get token: %w", err)
	}
	var res AuthResult
	if err := conn.Call(ctx, method, AuthParams{Token: tok}, &res); err != nil {
		return AuthResult{}, err
	}
	// Clients record who they are; only servers enforce token expiry.
	conn.setIdentity(auth.Identity{Subject: res.Subject}, time.Time{})
	return res, nil
}

// refreshLoop renews the token at refreshAt of its remaining lifetime.
func (c *Conn) refreshLoop(res AuthResult, token func(context.Context) (string, error)) {
	for res.ExpiresAt > 0 {
		wait := time.Duration(float64(time.Until(time.Unix(res.ExpiresAt, 0))) * refreshAt)
		if wait < 0 {
			wait = 0
		}
		select {
		case <-c.done:
			return
		case <-time.After(wait):
		}
		next, err := authCall(c.ctx, c, MethodAuthRefresh, token)
		if err != nil {
			c.opts.Logger.Warn("rpc: token refresh failed", "error", err)
			select {
			case <-c.done:
				return
			case <-time.After(time.Second):
				continue // retry until the server closes the connection
			}
		}
		res = next
	}
}
