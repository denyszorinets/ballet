package feature_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func valid() feature.Feature {
	return feature.Feature{Title: "Invoice export", Description: "Exports invoices as CSV.", Status: feature.StatusPlanned}
}

func TestFeature_Validate(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*feature.Feature)
		wantErr string
	}{
		{name: "valid"},
		{name: "empty title", change: func(f *feature.Feature) { f.Title = " " }, wantErr: "title"},
		{name: "long title", change: func(f *feature.Feature) { f.Title = strings.Repeat("x", 301) }, wantErr: "title"},
		{name: "long description", change: func(f *feature.Feature) { f.Description = strings.Repeat("x", 100_001) }, wantErr: "description"},
		{name: "unknown status", change: func(f *feature.Feature) { f.Status = "shipped" }, wantErr: "status"},
		{name: "duplicate project", change: func(f *feature.Feature) { f.ProjectIDs = []string{"p1", "p1"} }, wantErr: "twice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := valid()
			if tt.change != nil {
				tt.change(&f)
			}
			err := f.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestParseKey(t *testing.T) {
	n, err := feature.ParseKey("F-12")
	assert.NoError(t, err)
	assert.Equal(t, int64(12), n)
	assert.Equal(t, "F-12", feature.Key(12))
	for _, bad := range []string{"", "F-0", "F12", "f-1", "WEB-1", "F-x"} {
		_, err := feature.ParseKey(bad)
		assert.Error(t, err, bad)
	}
}

func TestLink_Validate(t *testing.T) {
	assert.NoError(t, feature.Link{FromID: "a", ToID: "b", Type: feature.LinkDerivedFrom}.Validate())
	assert.ErrorContains(t, feature.Link{FromID: "a", ToID: "a", Type: feature.LinkRelates}.Validate(), "itself")
	assert.ErrorContains(t, feature.Link{FromID: "a", ToID: "b", Type: "parent_of"}.Validate(), "type")
}

func TestDeliveryStatus_FollowsLinkedTickets(t *testing.T) {
	tests := []struct {
		name    string
		current feature.Status
		tickets []tracker.State
		want    feature.Status
	}{
		{"no tickets keeps the status", feature.StatusPlanned, nil, feature.StatusPlanned},
		{"planned with backlog tickets stays planned", feature.StatusPlanned, []tracker.State{tracker.StateBacklog}, feature.StatusPlanned},
		{"a started ticket starts a planned feature", feature.StatusPlanned, []tracker.State{tracker.StateInProgress, tracker.StateBacklog}, feature.StatusInProgress},
		{"a waiting ticket counts as started", feature.StatusPlanned, []tracker.State{tracker.StateWaitingForAnswer}, feature.StatusInProgress},
		{"all done makes it live", feature.StatusInProgress, []tracker.State{tracker.StateDone, tracker.StateCancelled}, feature.StatusLive},
		{"work on a live feature makes it changing", feature.StatusLive, []tracker.State{tracker.StateDone, tracker.StateInProgress}, feature.StatusChanging},
		{"changing goes back to live when done", feature.StatusChanging, []tracker.State{tracker.StateDone, tracker.StateDone}, feature.StatusLive},
		{"only cancelled tickets do not make it live", feature.StatusInProgress, []tracker.State{tracker.StateCancelled}, feature.StatusInProgress},
		{"deprecated is set by humans and kept", feature.StatusDeprecated, []tracker.State{tracker.StateInProgress}, feature.StatusDeprecated},
		{"removed is kept", feature.StatusRemoved, []tracker.State{tracker.StateDone}, feature.StatusRemoved},
		{"unfinished ready work does not make it live", feature.StatusInProgress, []tracker.State{tracker.StateDone, tracker.StateReady}, feature.StatusInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, feature.DeliveryStatus(tt.current, tt.tickets))
		})
	}
}
