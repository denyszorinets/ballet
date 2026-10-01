package tracker

import (
	"errors"
	"fmt"
	"time"
)

// DepType is the type of a dependency between two items.
type DepType string

// Dependency types.
const (
	// DepBlocks: the To item cannot start until the From item is resolved.
	// Blocking edges drive scheduling and must form a directed acyclic graph.
	DepBlocks DepType = "blocks"
	// DepRelates: informational, undirected; feeds the knowledge lineage.
	DepRelates DepType = "relates"
)

// Dependency is an edge between two items of the same project.
type Dependency struct {
	ID        string
	ProjectID string
	FromID    string
	ToID      string
	Type      DepType
	CreatedAt time.Time
}

// Errors returned by dependency validation.
var (
	ErrSelfDependency = errors.New("an item cannot depend on itself")
	ErrCycle          = errors.New("dependency would create a cycle")
)

// Resolved reports whether an item in state s no longer blocks others.
func Resolved(s State) bool { return s == StateDone || s == StateCancelled }

// ValidateDependency checks a new edge against the existing edges of the
// project. For blocking edges it rejects cycles: adding from→to is invalid
// when "to" already reaches "from".
func ValidateDependency(existing []Dependency, d Dependency) error {
	if d.Type != DepBlocks && d.Type != DepRelates {
		return fmt.Errorf("dependency type %q must be blocks or relates", d.Type)
	}
	if d.FromID == d.ToID {
		return ErrSelfDependency
	}
	if d.Type == DepRelates {
		return nil
	}
	next := map[string][]string{}
	for _, e := range existing {
		if e.Type == DepBlocks {
			next[e.FromID] = append(next[e.FromID], e.ToID)
		}
	}
	// Depth-first search from d.ToID looking for d.FromID.
	seen := map[string]bool{}
	stack := []string{d.ToID}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == d.FromID {
			return ErrCycle
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, next[n]...)
	}
	return nil
}

// NormalizeRelates orders the ends of an undirected edge so that each pair
// is stored once.
func NormalizeRelates(d Dependency) Dependency {
	if d.Type == DepRelates && d.FromID > d.ToID {
		d.FromID, d.ToID = d.ToID, d.FromID
	}
	return d
}
