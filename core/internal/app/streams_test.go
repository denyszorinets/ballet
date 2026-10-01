package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

type collector struct {
	mu     sync.Mutex
	events []app.StreamEvent
}

func (c *collector) deliver(e app.StreamEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *collector) waitFor(t *testing.T, n int) []app.StreamEvent {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.events) >= n
	}, 5*time.Second, 5*time.Millisecond, "expected %d events", n)
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]app.StreamEvent{}, c.events...)
}

func newStreams(t *testing.T) (*app.Streams, *app.Tracker) {
	t.Helper()
	tr, env := newTracker(t)
	feed := &app.Feed{Log: env.store, Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = feed.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	// Let the feed initialise its position before tests write events.
	time.Sleep(20 * time.Millisecond)
	return &app.Streams{Feed: feed, Log: env.store, Items: env.store, Tenancy: env.store, Authz: env.rbac}, tr
}

func createTicket(t *testing.T, tr *app.Tracker, project, title string) app.ItemView {
	t.Helper()
	it, err := tr.CreateItem(user(t, "bob", "acme-devs"), app.CreateItemInput{ProjectKey: project, Kind: tracker.KindTicket, Title: title})
	require.NoError(t, err)
	return it
}

func TestStreams_ProjectStreamDeliversLiveEventsOfThatProject(t *testing.T) {
	st, tr := newStreams(t)
	var got collector
	sub, err := st.Subscribe(user(t, "bob", "acme-devs"), "project:WEB", nil, got.deliver)
	require.NoError(t, err)
	t.Cleanup(sub.Close)

	createTicket(t, tr, "APP", "other project")
	tk := createTicket(t, tr, "WEB", "mine")
	_, err = tr.TransitionItem(user(t, "bob", "acme-devs"), tk.Key, tracker.StateReady, tk.Version)
	require.NoError(t, err)

	events := got.waitFor(t, 2)
	require.Len(t, events, 2, "events of other projects are filtered out")
	assert.Equal(t, "item.created", events[0].Type)
	assert.Equal(t, "item.state_changed", events[1].Type)
	assert.Equal(t, tk.Key, events[0].EntityKey)
	assert.Greater(t, events[0].Seq, sub.Seq)
}

func TestStreams_ItemStreamOnlyCarriesThatItem(t *testing.T) {
	st, tr := newStreams(t)
	a := createTicket(t, tr, "WEB", "a")
	b := createTicket(t, tr, "WEB", "b")
	var got collector
	sub, err := st.Subscribe(user(t, "bob", "acme-devs"), "item:"+a.Key, nil, got.deliver)
	require.NoError(t, err)
	t.Cleanup(sub.Close)

	bob := user(t, "bob", "acme-devs")
	_, err = tr.TransitionItem(bob, b.Key, tracker.StateReady, b.Version)
	require.NoError(t, err)
	_, err = tr.TransitionItem(bob, a.Key, tracker.StateReady, a.Version)
	require.NoError(t, err)

	events := got.waitFor(t, 1)
	time.Sleep(30 * time.Millisecond)
	require.Len(t, got.waitFor(t, 1), 1)
	assert.Equal(t, a.Key, events[0].EntityKey)
}

func TestStreams_ResumeReplaysMissedEventsThenContinuesLive(t *testing.T) {
	st, tr := newStreams(t)
	bob := user(t, "bob", "acme-devs")
	first := createTicket(t, tr, "WEB", "before disconnect")
	var seen collector
	sub, err := st.Subscribe(bob, "project:WEB", nil, seen.deliver)
	require.NoError(t, err)
	lastSeen := sub.Seq
	sub.Close()
	<-sub.Done()

	// Missed while disconnected.
	missed1 := createTicket(t, tr, "WEB", "missed 1")
	missed2 := createTicket(t, tr, "WEB", "missed 2")

	var got collector
	resumed, err := st.Subscribe(bob, "project:WEB", &lastSeen, got.deliver)
	require.NoError(t, err)
	t.Cleanup(resumed.Close)
	live := createTicket(t, tr, "WEB", "live")

	events := got.waitFor(t, 3)
	var keys []string
	for _, e := range events {
		keys = append(keys, e.EntityKey)
	}
	assert.Equal(t, []string{missed1.Key, missed2.Key, live.Key}, keys, "no gaps, no duplicates, in order")
	assert.NotContains(t, keys, first.Key)
}

func TestStreams_ResumeTooFarBackRequiresResync(t *testing.T) {
	st, tr := newStreams(t)
	st.MaxReplay = 3
	bob := user(t, "bob", "acme-devs")
	from := int64(0)
	for i := range 4 {
		createTicket(t, tr, "WEB", fmt.Sprint(i))
	}

	_, err := st.Subscribe(bob, "project:WEB", &from, (&collector{}).deliver)

	assert.ErrorIs(t, err, app.ErrResyncRequired)
}

func TestStreams_ConcurrentWritesAreDeliveredExactlyOnceInOrder(t *testing.T) {
	st, tr := newStreams(t)
	var got collector
	sub, err := st.Subscribe(user(t, "bob", "acme-devs"), "project:WEB", nil, got.deliver)
	require.NoError(t, err)
	t.Cleanup(sub.Close)

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { createTicket(t, tr, "WEB", fmt.Sprint(i)) })
	}
	wg.Wait()

	events := got.waitFor(t, n)
	time.Sleep(30 * time.Millisecond)
	events = got.waitFor(t, n)
	require.Len(t, events, n)
	keys := map[string]bool{}
	for i, e := range events {
		keys[e.EntityKey] = true
		if i > 0 {
			assert.Greater(t, e.Seq, events[i-1].Seq)
		}
	}
	assert.Len(t, keys, n)
}

func TestStreams_Authorization(t *testing.T) {
	st, _ := newStreams(t)
	carol := user(t, "carol", "acme-viewers")

	sub, err := st.Subscribe(carol, "project:WEB", nil, (&collector{}).deliver)
	require.NoError(t, err)
	sub.Close()

	_, err = st.Subscribe(carol, "project:APP", nil, (&collector{}).deliver)
	assert.ErrorIs(t, err, app.ErrForbidden)
	_, err = st.Subscribe(carol, "team:x", nil, (&collector{}).deliver)
	assert.ErrorIs(t, err, app.ErrInvalid)
	_, err = st.Subscribe(carol, "project:NOPE", nil, (&collector{}).deliver)
	assert.ErrorIs(t, err, app.ErrNotFound)
}

func TestStreams_CloseReportsReason(t *testing.T) {
	st, _ := newStreams(t)
	sub, err := st.Subscribe(user(t, "bob", "acme-devs"), "project:WEB", nil, (&collector{}).deliver)
	require.NoError(t, err)

	sub.Close()

	assert.Equal(t, "unsubscribed", sub.Reason())
}
