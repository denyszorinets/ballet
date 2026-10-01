package tracker_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func ticket() tracker.Item {
	return tracker.Item{
		Kind: tracker.KindTicket, Title: "Export invoices", Type: tracker.TypeFeature,
		Policy: tracker.DefaultPolicy, AcceptanceCriteria: []string{"CSV has one row per invoice"},
	}
}

func TestItem_Validate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*tracker.Item)
		ok     bool
	}{
		{"valid ticket", func(*tracker.Item) {}, true},
		{"valid epic", func(it *tracker.Item) {
			*it = tracker.Item{Kind: tracker.KindEpic, Title: "Billing", MilestoneID: "m1"}
		}, true},
		{"valid milestone", func(it *tracker.Item) { *it = tracker.Item{Kind: tracker.KindMilestone, Title: "MVP"} }, true},
		{"unknown kind", func(it *tracker.Item) { it.Kind = "story" }, false},
		{"empty title", func(it *tracker.Item) { it.Title = "  " }, false},
		{"long title", func(it *tracker.Item) { it.Title = strings.Repeat("x", 301) }, false},
		{"bad ticket type", func(it *tracker.Item) { it.Type = "chore" }, false},
		{"bad review mode", func(it *tracker.Item) { it.Policy.ReviewMode = "human" }, false},
		{"bad merge mode", func(it *tracker.Item) { it.Policy.MergeMode = "yolo" }, false},
		{"empty criterion", func(it *tracker.Item) { it.AcceptanceCriteria = []string{""} }, false},
		{"too many criteria", func(it *tracker.Item) { it.AcceptanceCriteria = make([]string, 51) }, false},
		{"epic with type", func(it *tracker.Item) {
			*it = tracker.Item{Kind: tracker.KindEpic, Title: "E", Type: tracker.TypeBug}
		}, false},
		{"milestone in milestone", func(it *tracker.Item) {
			*it = tracker.Item{Kind: tracker.KindMilestone, Title: "M", MilestoneID: "m0"}
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := ticket()
			tt.mutate(&it)
			err := it.Validate()
			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		kind     tracker.Kind
		from, to tracker.State
		want     bool
	}{
		{tracker.KindTicket, tracker.StateBacklog, tracker.StateReady, true},
		{tracker.KindTicket, tracker.StateReady, tracker.StateBacklog, true},
		{tracker.KindTicket, tracker.StateInProgress, tracker.StatePaused, true},
		{tracker.KindTicket, tracker.StatePaused, tracker.StateReady, true},
		{tracker.KindTicket, tracker.StateDone, tracker.StateBacklog, true},
		{tracker.KindTicket, tracker.StateBacklog, tracker.StateInProgress, false},
		{tracker.KindTicket, tracker.StateReady, tracker.StateWaitingForAnswer, false},
		{tracker.KindTicket, tracker.StateInProgress, tracker.StateDone, false},
		{tracker.KindTicket, tracker.StateBacklog, tracker.StateOpen, false},
		{tracker.KindEpic, tracker.StateOpen, tracker.StateDone, true},
		{tracker.KindMilestone, tracker.StateCancelled, tracker.StateOpen, true},
		{tracker.KindMilestone, tracker.StateOpen, tracker.StateReady, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind)+"/"+string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			assert.Equal(t, tt.want, tracker.CanTransition(tt.kind, tt.from, tt.to))
		})
	}
}

func TestInitialState(t *testing.T) {
	assert.Equal(t, tracker.StateBacklog, tracker.InitialState(tracker.KindTicket))
	assert.Equal(t, tracker.StateOpen, tracker.InitialState(tracker.KindEpic))
	assert.Equal(t, tracker.StateOpen, tracker.InitialState(tracker.KindMilestone))
}
