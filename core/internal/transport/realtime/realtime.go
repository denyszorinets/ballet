// Package realtime serves Core's stateful API (ADR-0018): authenticated
// JSON-RPC over WebSocket at /rpc.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/rpc"
)

// Path is where the realtime API is served.
const Path = "/rpc"

// Deps are the collaborators of the realtime API.
type Deps struct {
	Verifier *oidc.Verifier
	Streams  *app.Streams
	Options  rpc.Options
	Now      func() time.Time
}

// Register mounts the realtime API on mux.
func Register(mux *http.ServeMux, d Deps) {
	h := &handlers{deps: d}
	opts := d.Options
	opts.Handler = h.handle
	mux.Handle("GET "+Path, rpc.NewServer(rpc.ServerOptions{
		Options:      opts,
		Authenticate: authenticator(d.Verifier),
		OnConnect:    h.onConnect,
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

// SubscribeParams are the params of stream.subscribe.
type SubscribeParams struct {
	Stream  string `json:"stream"`             // "project:<KEY>" or "item:<KEY>"
	FromSeq *int64 `json:"from_seq,omitempty"` // resume after this seq
}

// SubscribeResult is the result of stream.subscribe.
type SubscribeResult struct {
	Subscription string `json:"subscription"`
	Seq          int64  `json:"seq"` // event log position when subscribed
}

// UnsubscribeParams are the params of stream.unsubscribe.
type UnsubscribeParams struct {
	Subscription string `json:"subscription"`
}

// EventNotification is sent as stream.event.
type EventNotification struct {
	Subscription string          `json:"subscription"`
	Seq          int64           `json:"seq"`
	Type         string          `json:"type"`
	EntityType   string          `json:"entity_type"`
	EntityID     string          `json:"entity_id"`
	EntityKey    string          `json:"entity_key,omitempty"`
	OccurredAt   int64           `json:"occurred_at"` // Unix milliseconds
	Actor        event.Actor     `json:"actor"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// ClosedNotification is sent as stream.closed when the server ends a
// subscription ("lagging": resubscribe with from_seq).
type ClosedNotification struct {
	Subscription string `json:"subscription"`
	Reason       string `json:"reason"`
}

// CodeResyncRequired answers stream.subscribe when from_seq is too old.
const CodeResyncRequired = -32010

type connState struct {
	mu   sync.Mutex
	subs map[string]*app.Subscription
	next int
}

type handlers struct {
	deps  Deps
	conns sync.Map // *rpc.Conn → *connState
}

func (h *handlers) onConnect(c *rpc.Conn) {
	st := &connState{subs: map[string]*app.Subscription{}}
	h.conns.Store(c, st)
	go func() {
		<-c.Done()
		h.conns.Delete(c)
		st.mu.Lock()
		defer st.mu.Unlock()
		for _, s := range st.subs {
			s.Close()
		}
	}()
}

func (h *handlers) state(c *rpc.Conn) *connState {
	v, _ := h.conns.Load(c)
	st, _ := v.(*connState)
	return st
}

func (h *handlers) handle(ctx context.Context, req *rpc.Request) (any, error) {
	switch req.Method {
	case "system.ping":
		id, _ := auth.FromContext(ctx)
		return PingResult{Time: h.deps.Now().UnixMilli(), Subject: id.Subject}, nil
	case "stream.subscribe":
		var p SubscribeParams
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		return h.subscribe(ctx, req.Conn, p)
	case "stream.unsubscribe":
		var p UnsubscribeParams
		if err := req.Decode(&p); err != nil {
			return nil, err
		}
		st := h.state(req.Conn)
		st.mu.Lock()
		s, ok := st.subs[p.Subscription]
		delete(st.subs, p.Subscription)
		st.mu.Unlock()
		if !ok {
			return nil, rpc.Errorf(rpc.CodeNotFound, "subscription %s not found", p.Subscription)
		}
		s.Close()
		return struct{}{}, nil
	}
	return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
}

func (h *handlers) subscribe(ctx context.Context, c *rpc.Conn, p SubscribeParams) (any, error) {
	st := h.state(c)
	if st == nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "connection not registered")
	}
	st.mu.Lock()
	st.next++
	subID := fmt.Sprintf("s%d", st.next)
	st.mu.Unlock()

	deliver := func(e app.StreamEvent) error {
		return c.Notify(context.Background(), "stream.event", EventNotification{
			Subscription: subID, Seq: e.Seq, Type: e.Type, EntityType: e.EntityType, EntityID: e.EntityID,
			EntityKey: e.EntityKey, OccurredAt: e.OccurredAt.UnixMilli(), Actor: e.Actor, Payload: e.Payload,
		})
	}
	sub, err := h.deps.Streams.Subscribe(ctx, p.Stream, p.FromSeq, deliver)
	if err != nil {
		return nil, rpcError(err)
	}
	st.mu.Lock()
	st.subs[subID] = sub
	st.mu.Unlock()
	go func() {
		if reason := sub.Reason(); reason == "lagging" {
			_ = c.Notify(context.Background(), "stream.closed", ClosedNotification{Subscription: subID, Reason: reason})
		}
		st.mu.Lock()
		delete(st.subs, subID)
		st.mu.Unlock()
	}()
	return SubscribeResult{Subscription: subID, Seq: sub.Seq}, nil
}

// rpcError maps app errors to RPC error codes.
func rpcError(err error) error {
	switch {
	case errors.Is(err, app.ErrResyncRequired):
		return rpc.Errorf(CodeResyncRequired, "resync required: reload and subscribe without from_seq")
	case errors.Is(err, app.ErrInvalid):
		return rpc.Errorf(rpc.CodeInvalidParams, "%v", err)
	case errors.Is(err, app.ErrForbidden):
		return rpc.Errorf(rpc.CodeForbidden, "forbidden")
	case errors.Is(err, app.ErrNotFound):
		return rpc.Errorf(rpc.CodeNotFound, "%v", err)
	case errors.Is(err, app.ErrUnauthorized):
		return rpc.Errorf(rpc.CodeUnauthenticated, "unauthenticated")
	}
	return err
}
