package rpc_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/rpc"
)

type echoParams struct {
	Text  string   `json:"text"`
	Count int      `json:"count"`
	Tags  []string `json:"tags"`
}

// tokens maps valid tokens to subjects; "short:<subject>" tokens expire in
// 1 second.
func authenticate(_ context.Context, token string) (auth.Identity, time.Time, error) {
	if sub, ok := strings.CutPrefix(token, "short:"); ok {
		return auth.Identity{Kind: auth.KindHuman, Subject: sub}, time.Now().Add(time.Second), nil
	}
	if sub, ok := strings.CutPrefix(token, "valid:"); ok {
		return auth.Identity{Kind: auth.KindHuman, Subject: sub}, time.Time{}, nil
	}
	return auth.Identity{}, time.Time{}, errors.New("bad token")
}

func serverHandler(ctx context.Context, req *rpc.Request) (any, error) {
	switch req.Method {
	case "echo":
		var p echoParams
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		return p, nil
	case "whoami":
		id, _ := auth.FromContext(ctx)
		return id.Subject, nil
	case "fail":
		return nil, rpc.Errorf(rpc.CodeConflict, "stale version")
	case "crash":
		return nil, errors.New("secret internal detail")
	case "ping-back":
		// Server calls the client while handling a client request.
		var out string
		err := req.Conn.Call(ctx, "client.hello", "from server", &out)
		return out, err
	}
	return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
}

func newServer(t *testing.T, opts rpc.ServerOptions) (string, chan *rpc.Conn) {
	t.Helper()
	conns := make(chan *rpc.Conn, 10)
	if opts.Authenticate == nil {
		opts.Authenticate = authenticate
	}
	if opts.Handler == nil {
		opts.Handler = serverHandler
	}
	opts.OnConnect = func(c *rpc.Conn) { conns <- c }
	srv := httptest.NewServer(rpc.NewServer(opts))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), conns
}

func token(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}

func dial(t *testing.T, url string, opts rpc.DialOptions) *rpc.Conn {
	t.Helper()
	if opts.Token == nil {
		opts.Token = token("valid:alice")
	}
	c, _, err := rpc.Dial(t.Context(), url, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRPC_CallsAndErrorsOverBothCodecs(t *testing.T) {
	url, _ := newServer(t, rpc.ServerOptions{})
	for _, proto := range rpc.Subprotocols {
		t.Run(proto, func(t *testing.T) {
			c := dial(t, url, rpc.DialOptions{Subprotocols: []string{proto}, Options: rpc.Options{
				Handler: func(_ context.Context, req *rpc.Request) (any, error) {
					var s string
					_ = req.Decode(&s)
					return "client got " + s, nil
				},
			}})
			assert.Equal(t, proto, c.Subprotocol())
			assert.Equal(t, "alice", c.Identity().Subject)

			var out echoParams
			require.NoError(t, c.Call(t.Context(), "echo", echoParams{Text: "hi", Count: 3, Tags: []string{"a", "b"}}, &out))
			assert.Equal(t, echoParams{Text: "hi", Count: 3, Tags: []string{"a", "b"}}, out)

			var who string
			require.NoError(t, c.Call(t.Context(), "whoami", nil, &who))
			assert.Equal(t, "alice", who, "handlers see the authenticated identity")

			err := c.Call(t.Context(), "fail", nil, nil)
			assert.True(t, rpc.IsCode(err, rpc.CodeConflict))
			assert.ErrorContains(t, err, "stale version")

			err = c.Call(t.Context(), "crash", nil, nil)
			assert.True(t, rpc.IsCode(err, rpc.CodeInternal))
			assert.NotContains(t, err.Error(), "secret", "internal errors are not leaked")

			err = c.Call(t.Context(), "nope", nil, nil)
			assert.True(t, rpc.IsCode(err, rpc.CodeMethodNotFound))

			err = c.Call(t.Context(), "echo", "not an object", &out)
			assert.True(t, rpc.IsCode(err, rpc.CodeInvalidParams))

			var back string
			require.NoError(t, c.Call(t.Context(), "ping-back", nil, &back))
			assert.Equal(t, "client got from server", back, "connections are symmetric")
		})
	}
}

func TestRPC_ServerPushesNotifications(t *testing.T) {
	url, conns := newServer(t, rpc.ServerOptions{})
	got := make(chan string, 1)
	dial(t, url, rpc.DialOptions{Options: rpc.Options{Handler: func(_ context.Context, req *rpc.Request) (any, error) {
		var s string
		_ = req.Decode(&s)
		got <- req.Method + ":" + s
		return nil, nil
	}}})
	server := <-conns

	require.NoError(t, server.Notify(t.Context(), "ticket.changed", "WEB-1"))

	select {
	case msg := <-got:
		assert.Equal(t, "ticket.changed:WEB-1", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("notification not received")
	}
}

func TestRPC_ConcurrentCalls(t *testing.T) {
	url, _ := newServer(t, rpc.ServerOptions{})
	c := dial(t, url, rpc.DialOptions{})

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := range 50 {
		wg.Go(func() {
			var out echoParams
			err := c.Call(t.Context(), "echo", echoParams{Text: fmt.Sprint(i), Count: i}, &out)
			if err != nil || out.Count != i {
				failures.Add(1)
			}
		})
	}
	wg.Wait()

	assert.Zero(t, failures.Load())
}

func TestRPC_AuthenticationFailures(t *testing.T) {
	url, _ := newServer(t, rpc.ServerOptions{AuthTimeout: 200 * time.Millisecond})

	_, _, err := rpc.Dial(t.Context(), url, rpc.DialOptions{Token: token("forged")})
	assert.True(t, rpc.IsCode(err, rpc.CodeUnauthenticated), "invalid token: %v", err)

	// A raw client that never authenticates is disconnected after AuthTimeout.
	ws, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{Subprotocols: []string{rpc.SubprotocolJSON}})
	require.NoError(t, err)
	_, _, err = ws.Read(t.Context())
	assert.Equal(t, rpc.StatusUnauthenticated, websocket.CloseStatus(err))

	// A first message that is not auth is rejected.
	ws, _, err = websocket.Dial(t.Context(), url, &websocket.DialOptions{Subprotocols: []string{rpc.SubprotocolJSON}})
	require.NoError(t, err)
	require.NoError(t, ws.Write(t.Context(), websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"method":"echo","params":{}}`)))
	_, data, err := ws.Read(t.Context())
	require.NoError(t, err)
	assert.Contains(t, string(data), "-32001")
}

func TestRPC_RejectsMissingSubprotocol(t *testing.T) {
	url, _ := newServer(t, rpc.ServerOptions{})

	ws, _, err := websocket.Dial(t.Context(), url, nil)
	require.NoError(t, err)
	_, _, err = ws.Read(t.Context())

	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(err))
}

func TestRPC_TokenRefresh(t *testing.T) {
	opts := rpc.Options{HeartbeatInterval: 50 * time.Millisecond}
	url, conns := newServer(t, rpc.ServerOptions{Options: opts})
	var issued atomic.Int32
	c := dial(t, url, rpc.DialOptions{Options: opts, Token: func(context.Context) (string, error) {
		issued.Add(1)
		return "short:alice", nil
	}})
	server := <-conns

	// Tokens live 1s; the client refreshes at 80%. After 2.5s the connection
	// must still be open and several tokens issued.
	time.Sleep(2500 * time.Millisecond)

	require.NoError(t, c.Call(t.Context(), "whoami", nil, nil))
	assert.GreaterOrEqual(t, issued.Load(), int32(3))
	assert.NoError(t, server.Err())
}

func TestRPC_RefreshMustKeepSubject(t *testing.T) {
	url, _ := newServer(t, rpc.ServerOptions{})
	c := dial(t, url, rpc.DialOptions{})

	err := c.Call(t.Context(), rpc.MethodAuthRefresh, rpc.AuthParams{Token: "valid:mallory"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeUnauthenticated))

	var res rpc.AuthResult
	require.NoError(t, c.Call(t.Context(), rpc.MethodAuthRefresh, rpc.AuthParams{Token: "valid:alice"}, &res))
	assert.Equal(t, "alice", res.Subject)
}

func TestRPC_ExpiredTokenClosesConnection(t *testing.T) {
	// Generous heartbeat tolerance: the raw client sends none.
	url, conns := newServer(t, rpc.ServerOptions{Options: rpc.Options{HeartbeatInterval: 50 * time.Millisecond, HeartbeatMisses: 1000}})
	// A raw client authenticates with a short token and never refreshes.
	ws, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{Subprotocols: []string{rpc.SubprotocolJSON}})
	require.NoError(t, err)
	require.NoError(t, ws.Write(t.Context(), websocket.MessageText,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"auth","params":{"token":"short:alice"}}`)))
	server := <-conns

	select {
	case <-server.Done():
		assert.ErrorContains(t, server.Err(), "token expired")
	case <-time.After(5 * time.Second):
		t.Fatal("connection with an expired token was not closed")
	}
	_ = ws.CloseNow()
}

func TestRPC_SilentPeerIsDisconnected(t *testing.T) {
	url, conns := newServer(t, rpc.ServerOptions{Options: rpc.Options{HeartbeatInterval: 30 * time.Millisecond, HeartbeatMisses: 3}})
	ws, _, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{Subprotocols: []string{rpc.SubprotocolJSON}})
	require.NoError(t, err)
	require.NoError(t, ws.Write(t.Context(), websocket.MessageText,
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"auth","params":{"token":"valid:alice"}}`)))
	server := <-conns
	// The raw client never reads or sends again: no heartbeats.

	select {
	case <-server.Done():
		assert.ErrorContains(t, server.Err(), "heartbeat timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("silent peer was not disconnected")
	}
	_ = ws.CloseNow()
}

func TestRPC_HeartbeatsKeepIdleConnectionsAlive(t *testing.T) {
	opts := rpc.Options{HeartbeatInterval: 20 * time.Millisecond, HeartbeatMisses: 3}
	url, conns := newServer(t, rpc.ServerOptions{Options: opts})
	c := dial(t, url, rpc.DialOptions{Options: opts})
	server := <-conns

	time.Sleep(300 * time.Millisecond) // 15 intervals without application traffic

	assert.NoError(t, server.Err())
	assert.NoError(t, c.Err())
}

func TestRPC_CallsFailAfterClose(t *testing.T) {
	url, conns := newServer(t, rpc.ServerOptions{})
	c := dial(t, url, rpc.DialOptions{})
	server := <-conns

	require.NoError(t, server.Close())
	<-c.Done()

	assert.Error(t, c.Call(t.Context(), "echo", nil, nil))
}

var _ http.Handler = (*rpc.Server)(nil)
