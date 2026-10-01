package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
)

// EventQuery selects events from the event log. Zero fields do not filter.
type EventQuery struct {
	Project    string // project ID
	EntityType string
	EntityID   string
	AfterSeq   int64
	UpToSeq    int64
	Limit      int
}

// EventLog reads Core's append-only event log.
type EventLog interface {
	QueryEvents(ctx context.Context, q EventQuery) ([]event.Event, error)
	LastEventSeq(ctx context.Context) (int64, error)
}

// ErrResyncRequired means a subscription cannot be resumed from the given
// seq (too much was missed); reload the snapshot and subscribe again.
var ErrResyncRequired = errors.New("resync required")

// MaxReplay is the maximum number of events replayed when resuming.
const MaxReplay = 1000

// Feed tails the event log and fans new events out to subscriptions. Every
// write path appends events in its own batch; the feed sees all of them,
// in seq order, regardless of which component (or Core instance) wrote
// them.
type Feed struct {
	Log      EventLog
	Interval time.Duration // polling interval (default 200ms)
	Logger   *slog.Logger

	mu   sync.Mutex
	last int64
	subs map[*Subscription]struct{}
}

// Run polls the event log until ctx is cancelled.
func (f *Feed) Run(ctx context.Context) error {
	interval := f.Interval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	last, err := f.Log.LastEventSeq(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.last = last
	f.mu.Unlock()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := f.poll(ctx); err != nil && f.Logger != nil {
				f.Logger.WarnContext(ctx, "event feed poll failed", "error", err)
			}
		}
	}
}

func (f *Feed) poll(ctx context.Context) error {
	for {
		f.mu.Lock()
		last := f.last
		f.mu.Unlock()
		batch, err := f.Log.QueryEvents(ctx, EventQuery{AfterSeq: last, Limit: MaxReplay})
		if err != nil || len(batch) == 0 {
			return err
		}
		f.mu.Lock()
		f.last = batch[len(batch)-1].Seq
		for s := range f.subs {
			select {
			case s.batches <- batch:
			default:
				s.lag() // subscriber too slow: it closes and the client resumes
			}
		}
		f.mu.Unlock()
		if len(batch) < MaxReplay {
			return nil
		}
	}
}

func (f *Feed) attach(s *Subscription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs == nil {
		f.subs = map[*Subscription]struct{}{}
	}
	f.subs[s] = struct{}{}
}

func (f *Feed) detach(s *Subscription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, s)
}

// StreamEvent is an event delivered on a stream.
type StreamEvent struct {
	event.Event
	EntityKey string // item key for item events (e.g. WEB-42)
}

// Subscription delivers the events of one stream until closed.
type Subscription struct {
	Stream string
	Seq    int64 // event log position at subscription time

	batches chan []event.Event
	lagged  chan struct{}
	lagOnce sync.Once
	done    chan struct{}
	stop    context.CancelFunc
	closeMu sync.Once
	reason  string
}

func (s *Subscription) lag() { s.lagOnce.Do(func() { close(s.lagged) }) }

// Done is closed when the subscription ends; Reason says why.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason is why the subscription ended: "unsubscribed", "lagging" or "closed".
func (s *Subscription) Reason() string {
	<-s.done
	return s.reason
}

// Close ends the subscription.
func (s *Subscription) Close() { s.stop() }

// Streams implements stream subscriptions with authorization.
type Streams struct {
	// MaxReplay overrides the package MaxReplay (tests).
	MaxReplay int
	Feed      *Feed
	Log       EventLog
	Items     ItemStore
	Tenancy   TenancyStore
	Authz     Authorizer
}

// streamSpec is a resolved stream.
type streamSpec struct {
	query EventQuery
	match func(event.Event) bool
}

// Subscribe starts a subscription to stream ("project:<KEY>" or
// "item:<KEY>"). With fromSeq set, events after fromSeq are replayed first
// (ErrResyncRequired if more than MaxReplay); otherwise only new events are
// delivered. deliver is called sequentially; returning an error ends the
// subscription.
func (st *Streams) Subscribe(ctx context.Context, stream string, fromSeq *int64, deliver func(StreamEvent) error) (*Subscription, error) {
	spec, err := st.resolve(ctx, stream)
	if err != nil {
		return nil, err
	}
	runCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	s := &Subscription{
		Stream: stream, batches: make(chan []event.Event, 64), lagged: make(chan struct{}),
		done: make(chan struct{}), stop: stop,
	}
	// Attach before reading the position: every event after it then
	// arrives through the feed.
	st.Feed.attach(s)
	s0, err := st.Log.LastEventSeq(ctx)
	if err != nil {
		st.Feed.detach(s)
		stop()
		return nil, err
	}
	s.Seq = s0

	var replay []event.Event
	if fromSeq != nil && *fromSeq < s0 {
		q := spec.query
		limit := st.MaxReplay
		if limit <= 0 {
			limit = MaxReplay
		}
		q.AfterSeq, q.UpToSeq, q.Limit = *fromSeq, s0, limit+1
		if replay, err = st.Log.QueryEvents(ctx, q); err != nil {
			st.Feed.detach(s)
			stop()
			return nil, err
		}
		if len(replay) > limit {
			st.Feed.detach(s)
			stop()
			return nil, ErrResyncRequired
		}
	}

	go st.run(runCtx, s, spec, replay, deliver)
	return s, nil
}

func (st *Streams) run(ctx context.Context, s *Subscription, spec streamSpec, replay []event.Event, deliver func(StreamEvent) error) {
	reason := "closed"
	defer func() {
		st.Feed.detach(s)
		s.closeMu.Do(func() { s.reason = reason; close(s.done) })
	}()
	keys := map[string]string{}
	send := func(e event.Event) error {
		se := StreamEvent{Event: e}
		if e.EntityType == "item" {
			se.EntityKey = st.itemKey(ctx, keys, e.EntityID)
		}
		return deliver(se)
	}
	for _, e := range replay {
		if err := send(e); err != nil {
			return
		}
	}
	processed := s.Seq
	for {
		select {
		case <-ctx.Done():
			reason = "unsubscribed"
			return
		case <-s.lagged:
			reason = "lagging"
			return
		case batch := <-s.batches:
			for _, e := range batch {
				if e.Seq <= processed {
					continue
				}
				processed = e.Seq
				if spec.match(e) {
					if err := send(e); err != nil {
						return
					}
				}
			}
		}
	}
}

func (st *Streams) itemKey(ctx context.Context, cache map[string]string, id string) string {
	if k, ok := cache[id]; ok {
		return k
	}
	it, err := st.Items.ItemByID(ctx, id)
	if err != nil {
		return ""
	}
	cache[id] = it.Key
	return it.Key
}

// resolve parses a stream name and authorizes the caller.
func (st *Streams) resolve(ctx context.Context, stream string) (streamSpec, error) {
	id, err := caller(ctx)
	if err != nil {
		return streamSpec{}, err
	}
	kind, key, ok := strings.Cut(stream, ":")
	if !ok || key == "" {
		return streamSpec{}, fmt.Errorf("%w: stream %q must be project:<KEY> or item:<KEY>", ErrInvalid, stream)
	}
	var projectID string
	var spec streamSpec
	switch kind {
	case "project":
		p, err := st.Tenancy.ProjectByKey(ctx, key)
		if err != nil {
			return streamSpec{}, err
		}
		projectID = p.ID
		spec = streamSpec{
			query: EventQuery{Project: p.ID},
			match: func(e event.Event) bool { return e.Project == p.ID },
		}
	case "item":
		it, err := st.Items.ItemByKey(ctx, key)
		if err != nil {
			return streamSpec{}, err
		}
		projectID = it.ProjectID
		spec = streamSpec{
			query: EventQuery{EntityType: "item", EntityID: it.ID},
			match: func(e event.Event) bool { return e.EntityType == "item" && e.EntityID == it.ID },
		}
	default:
		return streamSpec{}, fmt.Errorf("%w: stream %q must be project:<KEY> or item:<KEY>", ErrInvalid, stream)
	}
	p, err := st.Tenancy.ProjectByID(ctx, projectID)
	if err != nil {
		return streamSpec{}, err
	}
	c, err := st.Tenancy.CustomerByID(ctx, p.CustomerID)
	if err != nil {
		return streamSpec{}, err
	}
	if err := st.Authz.Authorize(ctx, id, ActTrackerRead, Scope{Customer: c.Key, Project: p.Key}); err != nil {
		return streamSpec{}, err
	}
	return spec, nil
}
