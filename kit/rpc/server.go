package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/denyszorinets/ballet/kit/auth"
)

// Authenticator validates a token and returns the identity and token
// expiry (zero for none).
type Authenticator func(ctx context.Context, token string) (auth.Identity, time.Time, error)

// ServerOptions configure accepted connections.
type ServerOptions struct {
	Options
	Authenticate Authenticator
	// AuthTimeout bounds the wait for the first (auth) message (default 10s).
	AuthTimeout time.Duration
	// OriginPatterns allowed for cross-origin browsers (same origin always is).
	OriginPatterns []string
	// OnConnect is called for every authenticated connection before its
	// first request is handled; it must not block for long. The connection
	// is then served until it closes.
	OnConnect func(*Conn)
}

// Server is an http.Handler that upgrades requests to authenticated RPC
// connections.
type Server struct {
	opts ServerOptions
}

// NewServer returns a server; Authenticate is required.
func NewServer(opts ServerOptions) *Server {
	if opts.AuthTimeout <= 0 {
		opts.AuthTimeout = 10 * time.Second
	}
	return &Server{opts: opts}
}

// ServeHTTP upgrades the request and serves the connection until it closes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.Accept(w, r)
	if err != nil {
		return // Accept has answered or closed
	}
	<-conn.Done()
}

// Accept upgrades the request, negotiates the codec, authenticates the
// connection with its first message and calls OnConnect.
func (s *Server) Accept(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: Subprotocols, OriginPatterns: s.opts.OriginPatterns,
	})
	if err != nil {
		return nil, err
	}
	c, err := codecFor(ws.Subprotocol())
	if err != nil {
		_ = ws.Close(websocket.StatusPolicyViolation, "subprotocol required: "+SubprotocolMsgpack+" or "+SubprotocolJSON)
		return nil, err
	}
	conn := newConn(ws, c, s.opts.Options)
	if err := s.authenticate(conn); err != nil {
		conn.closeWith(StatusUnauthenticated, "unauthenticated", err)
		return nil, err
	}
	conn.intercept = s.interceptor(conn)
	// Before reading further messages: the client may send its next request
	// right after the auth response.
	if s.opts.OnConnect != nil {
		s.opts.OnConnect(conn)
	}
	conn.start()
	return conn, nil
}

// authenticate reads the first message, which must be an auth request.
// Without one within AuthTimeout the connection is closed with
// StatusUnauthenticated. (A read deadline cannot be used: the websocket
// library closes the connection abnormally when a read's context expires.)
func (s *Server) authenticate(conn *Conn) error {
	timer := time.AfterFunc(s.opts.AuthTimeout, func() {
		_ = conn.ws.Close(StatusUnauthenticated, "authentication timeout")
	})
	defer timer.Stop()
	ctx := context.Background()
	_, data, err := conn.ws.Read(ctx)
	if err != nil {
		return fmt.Errorf("rpc: waiting for auth: %w", err)
	}
	m, err := conn.codec.decode(data)
	if err != nil || !m.isRequest() || m.Method != MethodAuth {
		if err == nil && m.ID != nil {
			conn.respond(m.ID, nil, Errorf(CodeUnauthenticated, "the first message must be %s", MethodAuth))
		}
		return errors.New("rpc: first message is not auth")
	}
	res, err := s.verify(ctx, conn, m.Params, "")
	if err != nil {
		conn.respond(m.ID, nil, err)
		return err
	}
	conn.respond(m.ID, res, nil)
	return nil
}

// interceptor handles auth.refresh on an open connection.
func (s *Server) interceptor(conn *Conn) func(message) bool {
	return func(m message) bool {
		if m.Method == MethodAuth && m.isRequest() {
			conn.respond(m.ID, nil, Errorf(CodeInvalidRequest, "already authenticated; use %s", MethodAuthRefresh))
			return true
		}
		if m.Method != MethodAuthRefresh || !m.isRequest() {
			return false
		}
		go func() {
			res, err := s.verify(conn.ctx, conn, m.Params, conn.Identity().Subject)
			conn.respond(m.ID, res, err)
		}()
		return true
	}
}

// verify authenticates params; with wantSubject set, the token must belong
// to the same subject.
func (s *Server) verify(ctx context.Context, conn *Conn, params []byte, wantSubject string) (AuthResult, error) {
	var p AuthParams
	if err := conn.codec.unmarshal(params, &p); err != nil || p.Token == "" {
		return AuthResult{}, Errorf(CodeUnauthenticated, "token required")
	}
	id, expiry, err := s.opts.Authenticate(ctx, p.Token)
	if err != nil {
		return AuthResult{}, Errorf(CodeUnauthenticated, "invalid token")
	}
	if wantSubject != "" && id.Subject != wantSubject {
		return AuthResult{}, Errorf(CodeUnauthenticated, "token belongs to another subject")
	}
	conn.setIdentity(id, expiry)
	res := AuthResult{Subject: id.Subject}
	if !expiry.IsZero() {
		res.ExpiresAt = expiry.Unix()
	}
	return res, nil
}
