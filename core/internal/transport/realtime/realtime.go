// Package realtime serves Core's stateful API (ADR-0018): authenticated
// JSON-RPC over WebSocket at /rpc.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/event"

	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/rpc"
)

// Path is where the realtime API is served.
const Path = "/rpc"

// Deps are the collaborators of the realtime API.
type Deps struct {
	Verifier TokenVerifier
	Streams  *app.Streams
	Planner  *app.Planner // nil: planner methods not served
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
// TokenVerifier verifies access tokens (oidc.Verifier, or local.Authenticator
// without authentication).
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (auth.Identity, error)
}

func authenticator(v TokenVerifier) rpc.Authenticator {
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
	// Subscription is the client-chosen subscription ID, unique on the
	// connection. Events can arrive before the subscribe response (replay
	// starts immediately), so the client must know the ID up front.
	Subscription string `json:"subscription"`
	Stream       string `json:"stream"`             // "project:<KEY>" or "item:<KEY>"
	FromSeq      *int64 `json:"from_seq,omitempty"` // resume after this seq
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

// subscription is a stream subscription or a planner watch.
type subscription interface{ Close() }

type connState struct {
	mu   sync.Mutex
	subs map[string]subscription // nil value: ID reserved while subscribing
}

type handlers struct {
	deps  Deps
	conns sync.Map // *rpc.Conn → *connState
}

func (h *handlers) onConnect(c *rpc.Conn) {
	st := &connState{subs: map[string]subscription{}}
	h.conns.Store(c, st)
	go func() {
		<-c.Done()
		h.conns.Delete(c)
		st.mu.Lock()
		defer st.mu.Unlock()
		for _, s := range st.subs {
			if s != nil {
				s.Close()
			}
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
		if !ok || s == nil {
			return nil, rpc.Errorf(rpc.CodeNotFound, "subscription %s not found", p.Subscription)
		}
		s.Close()
		return struct{}{}, nil
	}
	if h.deps.Planner != nil {
		switch req.Method {
		case "planner.send":
			var p PlannerSendParams
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			m, err := h.deps.Planner.Send(ctx, p.Session, p.Text)
			if err != nil {
				return nil, rpcError(err)
			}
			return PlannerSendResult{Seq: m.Seq}, nil
		case "planner.cancel":
			var p PlannerSessionParams
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			if err := h.deps.Planner.Cancel(ctx, p.Session); err != nil {
				return nil, rpcError(err)
			}
			return struct{}{}, nil
		case "planner.watch":
			var p PlannerWatchParams
			if err := req.Decode(&p); err != nil {
				return nil, err
			}
			return h.watch(ctx, req.Conn, p)
		}
	}
	return nil, rpc.Errorf(rpc.CodeMethodNotFound, "method %s not found", req.Method)
}

// PlannerSendParams are the params of planner.send.
type PlannerSendParams struct {
	Session string `json:"session"`
	Text    string `json:"text"`
}

// PlannerSendResult is the result of planner.send: the stored message.
type PlannerSendResult struct {
	Seq int64 `json:"seq"`
}

// PlannerSessionParams are the params of planner.cancel.
type PlannerSessionParams struct {
	Session string `json:"session"`
}

// PlannerWatchParams are the params of planner.watch.
type PlannerWatchParams struct {
	Subscription string `json:"subscription"` // client-chosen; ends with stream.unsubscribe
	Session      string `json:"session"`
}

// PlannerWatchResult is the result of planner.watch.
type PlannerWatchResult struct {
	Subscription string `json:"subscription"`
	Running      bool   `json:"running"` // a turn is in progress
}

// PlannerOutputNotification is sent as planner.output.
type PlannerOutputNotification struct {
	Subscription string          `json:"subscription"`
	Session      string          `json:"session"`
	Type         string          `json:"type"` // text, tool_call, tool_result, message, done, error
	Text         string          `json:"text,omitempty"`
	Tool         string          `json:"tool,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	Seq          int64           `json:"seq,omitempty"`
}

// reserve claims a client-chosen subscription ID on the connection.
func (h *handlers) reserve(c *rpc.Conn, subID string) (*connState, error) {
	st := h.state(c)
	if st == nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "connection not registered")
	}
	if subID == "" || len(subID) > 64 {
		return nil, rpc.Errorf(rpc.CodeInvalidParams, "subscription must be a client-chosen ID of 1-64 characters")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, taken := st.subs[subID]; taken {
		return nil, rpc.Errorf(rpc.CodeInvalidParams, "subscription %s already exists on this connection", subID)
	}
	st.subs[subID] = nil
	return st, nil
}

func (h *handlers) watch(ctx context.Context, c *rpc.Conn, p PlannerWatchParams) (any, error) {
	st, err := h.reserve(c, p.Subscription)
	if err != nil {
		return nil, err
	}
	subID := p.Subscription
	w, running, err := h.deps.Planner.Watch(ctx, p.Session, func(o app.PlannerOutput) {
		_ = c.Notify(context.Background(), "planner.output", PlannerOutputNotification{
			Subscription: subID, Session: o.Session, Type: o.Type, Text: o.Text, Tool: o.Tool,
			ToolUseID: o.ToolUseID, Input: o.Input, IsError: o.IsError, Seq: o.Seq,
		})
	})
	if err != nil {
		st.mu.Lock()
		delete(st.subs, subID)
		st.mu.Unlock()
		return nil, rpcError(err)
	}
	st.mu.Lock()
	st.subs[subID] = w
	st.mu.Unlock()
	go func() {
		<-w.Done()
		if w.Lagging() {
			_ = c.Notify(context.Background(), "stream.closed", ClosedNotification{Subscription: subID, Reason: "lagging"})
		}
		st.mu.Lock()
		if st.subs[subID] == w {
			delete(st.subs, subID)
		}
		st.mu.Unlock()
	}()
	return PlannerWatchResult{Subscription: subID, Running: running}, nil
}

func (h *handlers) subscribe(ctx context.Context, c *rpc.Conn, p SubscribeParams) (any, error) {
	st, err := h.reserve(c, p.Subscription)
	if err != nil {
		return nil, err
	}
	subID := p.Subscription

	deliver := func(e app.StreamEvent) error {
		return c.Notify(context.Background(), "stream.event", EventNotification{
			Subscription: subID, Seq: e.Seq, Type: e.Type, EntityType: e.EntityType, EntityID: e.EntityID,
			EntityKey: e.EntityKey, OccurredAt: e.OccurredAt.UnixMilli(), Actor: e.Actor, Payload: e.Payload,
		})
	}
	sub, err := h.deps.Streams.Subscribe(ctx, p.Stream, p.FromSeq, deliver)
	if err != nil {
		st.mu.Lock()
		delete(st.subs, subID)
		st.mu.Unlock()
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
		if st.subs[subID] == subscription(sub) {
			delete(st.subs, subID)
		}
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
	case errors.Is(err, app.ErrConflict):
		return rpc.Errorf(rpc.CodeConflict, "%v", err)
	}
	return err
}
