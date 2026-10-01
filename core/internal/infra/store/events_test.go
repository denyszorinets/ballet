package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
	"github.com/denyszorinets/ballet/kit/sqlstore"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), t.TempDir()+"/core.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ticketEvent(entityID, typ string) event.Event {
	return event.Event{
		Customer: "c1", Project: "p1", EntityType: "ticket", EntityID: entityID, Type: typ,
		Actor:   event.Actor{Kind: event.ActorHuman, Subject: "user-bob"},
		Payload: json.RawMessage(`{"title":"Export invoices"}`),
	}
}

func TestEvents_AppendAssignsMonotonicSeqAndRoundTrips(t *testing.T) {
	s := newStore(t)
	before := time.Now().Add(-time.Second)

	require.NoError(t, s.DB().Batch(t.Context(),
		s.AppendEvent(ticketEvent("t1", "ticket.created")),
		s.AppendEvent(ticketEvent("t1", "ticket.updated")),
	))
	require.NoError(t, s.DB().Batch(t.Context(), s.AppendEvent(ticketEvent("t2", "ticket.created"))))

	all, err := s.ListEvents(t.Context(), store.EventFilter{Project: "p1"})
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Less(t, all[0].Seq, all[1].Seq)
	assert.Less(t, all[1].Seq, all[2].Seq)
	assert.Equal(t, "ticket.created", all[0].Type)
	assert.NotEmpty(t, all[0].ID)
	assert.True(t, all[0].OccurredAt.After(before))
	assert.Equal(t, event.Actor{Kind: event.ActorHuman, Subject: "user-bob"}, all[0].Actor)
	assert.JSONEq(t, `{"title":"Export invoices"}`, string(all[0].Payload))
}

func TestEvents_FilterByEntityAndAfterSeq(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.DB().Batch(t.Context(),
		s.AppendEvent(ticketEvent("t1", "ticket.created")),
		s.AppendEvent(ticketEvent("t2", "ticket.created")),
		s.AppendEvent(ticketEvent("t1", "ticket.updated")),
	))

	history, err := s.ListEvents(t.Context(), store.EventFilter{EntityType: "ticket", EntityID: "t1"})
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, []string{"ticket.created", "ticket.updated"}, []string{history[0].Type, history[1].Type})

	after, err := s.ListEvents(t.Context(), store.EventFilter{Project: "p1", AfterSeq: history[0].Seq})
	require.NoError(t, err)
	assert.Len(t, after, 2)

	limited, err := s.ListEvents(t.Context(), store.EventFilter{Project: "p1", Limit: 1})
	require.NoError(t, err)
	assert.Len(t, limited, 1)
}

func TestEvents_AreNotWrittenWhenTheBatchFails(t *testing.T) {
	s := newStore(t)

	err := s.DB().Batch(t.Context(),
		s.AppendEvent(ticketEvent("t1", "ticket.created")),
		sqlstore.Exec("INSERT INTO no_such_table VALUES (1)"),
	)

	require.Error(t, err)
	all, err := s.ListEvents(t.Context(), store.EventFilter{Project: "p1"})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestEvents_LastSeq(t *testing.T) {
	s := newStore(t)
	seq, err := s.LastEventSeq(t.Context())
	require.NoError(t, err)
	assert.Zero(t, seq)

	require.NoError(t, s.DB().Batch(t.Context(), s.AppendEvent(ticketEvent("t1", "ticket.created"))))

	seq, err = s.LastEventSeq(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(1), seq)
}
