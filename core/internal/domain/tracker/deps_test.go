package tracker_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func blocks(from, to string) tracker.Dependency {
	return tracker.Dependency{FromID: from, ToID: to, Type: tracker.DepBlocks}
}

func TestValidateDependency(t *testing.T) {
	// a → b → c, and d (unconnected); x relates y.
	graph := []tracker.Dependency{
		blocks("a", "b"), blocks("b", "c"),
		{FromID: "x", ToID: "y", Type: tracker.DepRelates},
	}
	tests := []struct {
		name string
		dep  tracker.Dependency
		want error
	}{
		{"extends chain", blocks("c", "d"), nil},
		{"parallel edge", blocks("a", "c"), nil},
		{"direct cycle", blocks("b", "a"), tracker.ErrCycle},
		{"long cycle", blocks("c", "a"), tracker.ErrCycle},
		{"self", blocks("a", "a"), tracker.ErrSelfDependency},
		{"relates never cycles", tracker.Dependency{FromID: "c", ToID: "a", Type: tracker.DepRelates}, nil},
		{"relates edges do not count for cycles", blocks("y", "x"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tracker.ValidateDependency(graph, tt.dep)
			if tt.want == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.want)
			}
		})
	}
	assert.Error(t, tracker.ValidateDependency(graph, tracker.Dependency{FromID: "a", ToID: "d", Type: "needs"}))
}

func TestNormalizeRelates(t *testing.T) {
	d := tracker.NormalizeRelates(tracker.Dependency{FromID: "b", ToID: "a", Type: tracker.DepRelates})
	assert.Equal(t, "a", d.FromID)
	b := tracker.NormalizeRelates(blocks("b", "a"))
	assert.Equal(t, "b", b.FromID, "blocking edges keep their direction")
}

func TestResolved(t *testing.T) {
	assert.True(t, tracker.Resolved(tracker.StateDone))
	assert.True(t, tracker.Resolved(tracker.StateCancelled))
	assert.False(t, tracker.Resolved(tracker.StateReady))
	assert.False(t, tracker.Resolved(tracker.StateOpen))
}
