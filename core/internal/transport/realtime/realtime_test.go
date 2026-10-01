package realtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/domain/rbac"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/core/internal/transport/realtime"
	"github.com/denyszorinets/ballet/kit/auth"
	"github.com/denyszorinets/ballet/kit/auth/oidc"
	"github.com/denyszorinets/ballet/kit/auth/oidctest"
	"github.com/denyszorinets/ballet/kit/rpc"
)

type env struct {
	url     string
	iss     *oidctest.Issuer
	tracker *app.Tracker
	planner *app.Planner
}

// echoLLM answers every request with "You said: <last user text>".
type echoLLM struct{}

func (echoLLM) Stream(_ context.Context, req app.LLMRequest, onText func(string)) (app.LLMResponse, error) {
	last := req.Messages[len(req.Messages)-1].Content
	text := "You said: " + last[len(last)-1].Text
	onText(text)
	return app.LLMResponse{Content: []planner.Block{planner.Text(text)}, StopReason: "end_turn"}, nil
}

func setup(t *testing.T) env {
	t.Helper()
	iss := oidctest.NewIssuer(t)
	v, err := oidc.NewVerifier(t.Context(), oidc.Config{IssuerURL: iss.URL, Audience: "ballet"})
	require.NoError(t, err)
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	boot, err := rbac.ParseBootstrap("groups:admins")
	require.NoError(t, err)
	authz := &app.RBAC{Store: st, Bootstrap: []rbac.Binding{boot}}
	ten := &app.Tenancy{Store: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	_, err = ten.CreateCustomer(admin, app.CreateCustomerInput{Key: "acme", Name: "Acme"})
	require.NoError(t, err)
	_, err = ten.CreateProject(admin, app.CreateProjectInput{CustomerKey: "acme", Key: "WEB", Name: "Web"})
	require.NoError(t, err)

	feed := &app.Feed{Log: st, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = feed.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	time.Sleep(20 * time.Millisecond)

	pl := &app.Planner{Store: st, Tenancy: st, Authz: authz, LLM: echoLLM{}, Model: "m", MaxTokens: 100,
		Now: time.Now, NewID: store.NewID, Context: t.Context()}
	mux := http.NewServeMux()
	realtime.Register(mux, realtime.Deps{
		Verifier: v, Now: time.Now,
		Streams: &app.Streams{Feed: feed, Log: st, Items: st, Tenancy: st, Authz: authz, MaxReplay: 5},
		Planner: pl,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	tr := &app.Tracker{Items: st, Deps: st, Tenancy: st, Events: st, Authz: authz, Now: time.Now, NewID: store.NewID}
	return env{url: "ws" + strings.TrimPrefix(srv.URL, "http") + realtime.Path, iss: iss, tracker: tr, planner: pl}
}

func (e env) create(t *testing.T, title string) app.ItemView {
	t.Helper()
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	it, err := e.tracker.CreateItem(admin, app.CreateItemInput{ProjectKey: "WEB", Kind: tracker.KindTicket, Title: title})
	require.NoError(t, err)
	return it
}

type inbox struct {
	mu      sync.Mutex
	events  []realtime.EventNotification
	outputs []realtime.PlannerOutputNotification
}

func (b *inbox) handler(_ context.Context, req *rpc.Request) (any, error) {
	if req.Method == "stream.event" {
		var n realtime.EventNotification
		if err := req.Decode(&n); err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.events = append(b.events, n)
		b.mu.Unlock()
	}
	if req.Method == "planner.output" {
		var n realtime.PlannerOutputNotification
		if err := req.Decode(&n); err != nil {
			return nil, err
		}
		b.mu.Lock()
		b.outputs = append(b.outputs, n)
		b.mu.Unlock()
	}
	return nil, nil
}

func (b *inbox) keys(t *testing.T, n int) []string {
	t.Helper()
	require.Eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.events) >= n
	}, 5*time.Second, 5*time.Millisecond)
	b.mu.Lock()
	defer b.mu.Unlock()
	var keys []string
	for _, e := range b.events {
		keys = append(keys, e.EntityKey)
	}
	return keys
}

func (e env) dial(t *testing.T, subject string, groups []string, b *inbox) *rpc.Conn {
	t.Helper()
	tok := e.iss.Token(t, subject, "ballet", map[string]any{"groups": groups})
	c, _, err := rpc.Dial(t.Context(), e.url, rpc.DialOptions{
		Token:   func(context.Context) (string, error) { return tok, nil },
		Options: rpc.Options{Handler: b.handler},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRealtime_PingAndAuth(t *testing.T) {
	e := setup(t)
	c := e.dial(t, "user-1", nil, &inbox{})

	var pong realtime.PingResult
	require.NoError(t, c.Call(t.Context(), "system.ping", nil, &pong))
	assert.Equal(t, "user-1", pong.Subject)

	_, _, err := rpc.Dial(t.Context(), e.url, rpc.DialOptions{Token: func(context.Context) (string, error) { return "forged", nil }})
	assert.True(t, rpc.IsCode(err, rpc.CodeUnauthenticated))
}

func TestRealtime_SubscribeReceiveAndResume(t *testing.T) {
	e := setup(t)
	var first inbox
	c := e.dial(t, "alice", []string{"admins"}, &first)

	var sub realtime.SubscribeResult
	require.NoError(t, c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "a", Stream: "project:WEB"}, &sub))
	a := e.create(t, "a")
	assert.Equal(t, []string{a.Key}, first.keys(t, 1))
	lastSeq := first.events[0].Seq

	// Disconnect, miss two events, reconnect and resume.
	require.NoError(t, c.Close())
	b := e.create(t, "b")
	cc := e.create(t, "c")

	var second inbox
	c2 := e.dial(t, "alice", []string{"admins"}, &second)
	require.NoError(t, c2.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "b", Stream: "project:WEB", FromSeq: &lastSeq}, &sub))
	d := e.create(t, "d")
	assert.Equal(t, []string{b.Key, cc.Key, d.Key}, second.keys(t, 3))

	require.NoError(t, c2.Call(t.Context(), "stream.unsubscribe", realtime.UnsubscribeParams{Subscription: sub.Subscription}, nil))
	err := c2.Call(t.Context(), "stream.unsubscribe", realtime.UnsubscribeParams{Subscription: sub.Subscription}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeNotFound))
}

func TestRealtime_SubscribeErrors(t *testing.T) {
	e := setup(t)
	for range 6 {
		e.create(t, "x")
	}
	c := e.dial(t, "alice", []string{"admins"}, &inbox{})
	zero := int64(0)

	err := c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "z", Stream: "project:WEB", FromSeq: &zero}, nil)
	assert.True(t, rpc.IsCode(err, realtime.CodeResyncRequired), "%v", err)

	stranger := e.dial(t, "eve", nil, &inbox{})
	err = stranger.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "s", Stream: "project:WEB"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeForbidden), "%v", err)

	err = c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "x", Stream: "bogus"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeInvalidParams), "%v", err)
	err = c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Stream: "project:WEB"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeInvalidParams), "subscription ID is required: %v", err)
	require.NoError(t, c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "dup", Stream: "project:WEB"}, nil))
	err = c.Call(t.Context(), "stream.subscribe", realtime.SubscribeParams{Subscription: "dup", Stream: "project:WEB"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeInvalidParams), "IDs are unique per connection: %v", err)
}

func TestRealtime_PlannerChat(t *testing.T) {
	e := setup(t)
	admin := auth.WithIdentity(t.Context(), auth.Identity{Kind: auth.KindHuman, Subject: "alice", Claims: map[string]any{"groups": []any{"admins"}}})
	session, err := e.planner.CreateSession(admin, "WEB", "Chat")
	require.NoError(t, err)

	var box inbox
	c := e.dial(t, "alice", []string{"admins"}, &box)
	var w realtime.PlannerWatchResult
	require.NoError(t, c.Call(t.Context(), "planner.watch", realtime.PlannerWatchParams{Subscription: "w", Session: session.ID}, &w))
	assert.False(t, w.Running)
	var sent realtime.PlannerSendResult
	require.NoError(t, c.Call(t.Context(), "planner.send", realtime.PlannerSendParams{Session: session.ID, Text: "hello"}, &sent))
	assert.Equal(t, int64(1), sent.Seq)

	require.Eventually(t, func() bool {
		box.mu.Lock()
		defer box.mu.Unlock()
		return len(box.outputs) > 0 && box.outputs[len(box.outputs)-1].Type == app.OutputDone
	}, 5*time.Second, 5*time.Millisecond)
	box.mu.Lock()
	var kinds []string
	for _, o := range box.outputs {
		kinds = append(kinds, o.Type)
		assert.Equal(t, "w", o.Subscription)
	}
	assert.Equal(t, "You said: hello", box.outputs[1].Text)
	box.mu.Unlock()
	assert.Equal(t, []string{"message", "text", "message", "done"}, kinds)

	require.NoError(t, c.Call(t.Context(), "planner.cancel", realtime.PlannerSessionParams{Session: session.ID}, nil))
	require.NoError(t, c.Call(t.Context(), "stream.unsubscribe", realtime.UnsubscribeParams{Subscription: "w"}, nil))

	stranger := e.dial(t, "eve", nil, &inbox{})
	err = stranger.Call(t.Context(), "planner.send", realtime.PlannerSendParams{Session: session.ID, Text: "hi"}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeForbidden), "%v", err)
	err = stranger.Call(t.Context(), "planner.watch", realtime.PlannerWatchParams{Subscription: "x", Session: session.ID}, nil)
	assert.True(t, rpc.IsCode(err, rpc.CodeForbidden), "%v", err)
}
