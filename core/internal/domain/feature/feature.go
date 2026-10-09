// Package feature models the feature map (ADR-0028): an organization's
// features, their immutable revisions and the typed links between them.
// Tickets carry out changes to features; a feature's delivery status
// follows its tickets.
package feature

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// Status is where a feature is in its life.
type Status string

// Statuses. Ballet moves a feature between planned, in_progress, live and
// changing as its tickets progress; humans and agents set deprecated and
// removed.
const (
	StatusPlanned    Status = "planned"
	StatusInProgress Status = "in_progress"
	StatusLive       Status = "live"
	StatusChanging   Status = "changing"
	StatusDeprecated Status = "deprecated"
	StatusRemoved    Status = "removed"
)

// Statuses lists every status in life order.
var Statuses = []Status{StatusPlanned, StatusInProgress, StatusLive, StatusChanging, StatusDeprecated, StatusRemoved}

// Feature is a product capability of an organization, possibly
// implemented by several of its projects.
type Feature struct {
	ID             string
	OrganizationID string
	Number         int64
	Key            string // "F-<Number>", unique within the organization
	Title          string
	Description    string // Markdown: what the feature does now
	Status         Status
	ProjectIDs     []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        int64 // the number of its latest revision
}

// Limits.
const (
	maxTitle       = 300
	maxDescription = 100_000
	maxReason      = 2_000
)

// Validate checks a feature's content.
func (f Feature) Validate() error {
	var errs []error
	if strings.TrimSpace(f.Title) == "" || utf8.RuneCountInString(f.Title) > maxTitle {
		errs = append(errs, fmt.Errorf("title must be 1-%d characters", maxTitle))
	}
	if utf8.RuneCountInString(f.Description) > maxDescription {
		errs = append(errs, fmt.Errorf("description must be at most %d characters", maxDescription))
	}
	if !slices.Contains(Statuses, f.Status) {
		errs = append(errs, fmt.Errorf("status %q must be one of %v", f.Status, Statuses))
	}
	seen := map[string]bool{}
	for _, p := range f.ProjectIDs {
		if seen[p] {
			errs = append(errs, fmt.Errorf("project %s is listed twice", p))
		}
		seen[p] = true
	}
	return errors.Join(errs...)
}

var keyRe = regexp.MustCompile(`^F-([1-9][0-9]*)$`)

// Key formats a feature number as its key.
func Key(n int64) string { return "F-" + strconv.FormatInt(n, 10) }

// ParseKey returns the number of a feature key such as "F-12".
func ParseKey(key string) (int64, error) {
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		return 0, fmt.Errorf("feature key %q must look like F-12", key)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

// Review is the review state of a revision.
type Review string

// Review states. Revisions by humans need no review; revisions by agents
// under the direct policy wait for one.
const (
	ReviewNone      Review = ""
	ReviewPending   Review = "pending"
	ReviewConfirmed Review = "confirmed"
	ReviewReverted  Review = "reverted"
)

// Cause is what led to a revision.
type Cause struct {
	Kind string // "changeset", "ticket", "run", "revert" or "" (a direct edit)
	Ref  string // its ID or key
}

// Revision is the immutable state of a feature after one change.
type Revision struct {
	FeatureID   string
	Number      int64
	Title       string
	Description string
	Status      Status
	ProjectIDs  []string
	Author      event.Actor
	Reason      string
	Cause       Cause
	Review      Review
	ReviewedBy  string
	ReviewedAt  time.Time
	CreatedAt   time.Time
}

// RevisionOf is the revision recording f's current state.
func RevisionOf(f Feature, author event.Actor, reason string, cause Cause, review Review) Revision {
	return Revision{
		FeatureID: f.ID, Number: f.Version, Title: f.Title, Description: f.Description, Status: f.Status,
		ProjectIDs: slices.Clone(f.ProjectIDs), Author: author, Reason: reason, Cause: cause, Review: review,
		CreatedAt: f.UpdatedAt,
	}
}

// ValidateReason checks a revision's reason.
func ValidateReason(reason string) error {
	if utf8.RuneCountInString(reason) > maxReason {
		return fmt.Errorf("reason must be at most %d characters", maxReason)
	}
	return nil
}

// LinkType is the meaning of a link from one feature to another.
type LinkType string

// Link types, read "From <type> To": F-7 derived_from F-2.
const (
	LinkDerivedFrom LinkType = "derived_from"
	LinkSplitFrom   LinkType = "split_from"
	LinkMergedInto  LinkType = "merged_into"
	LinkSupersedes  LinkType = "supersedes"
	LinkDependsOn   LinkType = "depends_on"
	LinkRelates     LinkType = "relates"
)

// LinkTypes lists every link type.
var LinkTypes = []LinkType{LinkDerivedFrom, LinkSplitFrom, LinkMergedInto, LinkSupersedes, LinkDependsOn, LinkRelates}

// Lineage reports whether a link makes From a descendant of To.
func (t LinkType) Lineage() bool { return t == LinkDerivedFrom || t == LinkSplitFrom }

// Link is a typed link between two features of an organization, valid
// from CreatedAt until RemovedAt (zero: still valid).
type Link struct {
	ID             string
	OrganizationID string
	FromID         string
	ToID           string
	Type           LinkType
	CreatedBy      event.Actor
	CreatedAt      time.Time
	RemovedBy      event.Actor
	RemovedAt      time.Time
}

// Validate checks a link's shape.
func (l Link) Validate() error {
	var errs []error
	if !slices.Contains(LinkTypes, l.Type) {
		errs = append(errs, fmt.Errorf("link type %q must be one of %v", l.Type, LinkTypes))
	}
	if l.FromID == l.ToID {
		errs = append(errs, errors.New("a feature cannot link to itself"))
	}
	return errors.Join(errs...)
}

// ValidAt reports whether the link existed at t.
func (l Link) ValidAt(t time.Time) bool {
	return !l.CreatedAt.After(t) && (l.RemovedAt.IsZero() || l.RemovedAt.After(t))
}

// DeliveryStatus is the status a feature in current should have given the
// states of the tickets that change it. Work started on a planned feature
// puts it in progress, and on a live one makes it changing; once every
// ticket is resolved and at least one is done, the feature is live.
// Deprecated and removed features, and features without tickets, keep
// their status.
func DeliveryStatus(current Status, tickets []tracker.State) Status {
	switch current {
	case StatusPlanned, StatusInProgress, StatusLive, StatusChanging:
	default:
		return current
	}
	if len(tickets) == 0 {
		return current
	}
	started, done, open := false, false, false
	for _, s := range tickets {
		switch s {
		case tracker.StateInProgress, tracker.StateWaitingForAnswer, tracker.StatePaused:
			started, open = true, true
		case tracker.StateDone:
			done = true
		case tracker.StateCancelled:
		default:
			open = true
		}
	}
	switch {
	case !open && done:
		return StatusLive
	case started && (current == StatusLive || current == StatusChanging):
		return StatusChanging
	case started:
		return StatusInProgress
	}
	return current
}
