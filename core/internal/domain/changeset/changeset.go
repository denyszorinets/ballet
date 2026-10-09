// Package changeset models plan changesets: batches of planning changes
// proposed by the planner (or a human) and approved by a human, wholly or
// operation by operation. Only approved operations are applied.
package changeset

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denyszorinets/ballet/core/internal/domain/event"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

// Status of a changeset.
type Status string

// Statuses. A changeset is decided once: applied (all or some operations)
// or rejected.
const (
	StatusProposed Status = "proposed"
	StatusApplied  Status = "applied"
	StatusRejected Status = "rejected"
)

// OpKind is the kind of an operation.
type OpKind string

// Operation kinds.
const (
	OpCreateItem    OpKind = "create_item"
	OpUpdateItem    OpKind = "update_item"
	OpAddDependency OpKind = "add_dependency"
	OpCreateFeature OpKind = "create_feature"
	OpUpdateFeature OpKind = "update_feature"
	OpLinkFeatures  OpKind = "link_features"
)

// kindFeature marks refs declared by create_feature operations.
const kindFeature tracker.Kind = "feature"

// An item reference in an operation is either the key of an existing item
// of the project ("WEB-12") or "$" followed by the Ref of a create_item
// operation earlier in the same changeset ("$login").

// CreateItem creates a milestone, epic or ticket.
type CreateItem struct {
	Kind               tracker.Kind       `json:"kind"`
	Title              string             `json:"title"`
	Description        string             `json:"description,omitempty"`
	Type               tracker.TicketType `json:"type,omitempty"`                // tickets; default feature
	AcceptanceCriteria []string           `json:"acceptance_criteria,omitempty"` // tickets
	Policy             *tracker.Policy    `json:"policy,omitempty"`              // tickets; default fully autonomous
	Epic               string             `json:"epic,omitempty"`                // tickets: item reference
	Milestone          string             `json:"milestone,omitempty"`           // tickets and epics: item reference
	Features           []string           `json:"features,omitempty"`            // tickets: features it changes (feature references)
}

// UpdateItem changes the non-nil fields of an existing item.
type UpdateItem struct {
	Item               string              `json:"item"` // key of an existing item
	Title              *string             `json:"title,omitempty"`
	Description        *string             `json:"description,omitempty"`
	Type               *tracker.TicketType `json:"type,omitempty"`
	AcceptanceCriteria *[]string           `json:"acceptance_criteria,omitempty"`
	Policy             *tracker.Policy     `json:"policy,omitempty"`
	Epic               *string             `json:"epic,omitempty"`      // item reference; "" removes the epic
	Milestone          *string             `json:"milestone,omitempty"` // item reference; "" removes the milestone
	Features           *[]string           `json:"features,omitempty"`  // tickets: replaces the features it changes
}

// A feature reference is the key of an existing feature of the
// organization ("F-3") or "$" followed by the Ref of a create_feature
// operation earlier in the same changeset.

// CreateFeature adds a feature to the organization's feature map
// (ADR-0028).
type CreateFeature struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Status      feature.Status `json:"status,omitempty"`   // default planned
	Projects    []string       `json:"projects,omitempty"` // project keys; default the changeset's project
}

// UpdateFeature changes the non-nil fields of an existing feature.
type UpdateFeature struct {
	Feature     string          `json:"feature"` // key of an existing feature
	Title       *string         `json:"title,omitempty"`
	Description *string         `json:"description,omitempty"`
	Status      *feature.Status `json:"status,omitempty"`
	Projects    *[]string       `json:"projects,omitempty"`
}

// LinkFeatures links two features: From <Type> To.
type LinkFeatures struct {
	From string           `json:"from"` // feature reference
	To   string           `json:"to"`   // feature reference
	Type feature.LinkType `json:"type"`
}

// AddDependency links two items: From blocks To, or they relate.
type AddDependency struct {
	From string          `json:"from"` // item reference
	To   string          `json:"to"`   // item reference
	Type tracker.DepType `json:"type"`
}

// Op is one proposed change. Exactly the payload matching Kind is set.
type Op struct {
	Kind          OpKind         `json:"kind"`
	Ref           string         `json:"ref,omitempty"` // create_item, create_feature: name later operations use as "$Ref"
	Create        *CreateItem    `json:"create,omitempty"`
	Update        *UpdateItem    `json:"update,omitempty"`
	Dependency    *AddDependency `json:"dependency,omitempty"`
	Feature       *CreateFeature `json:"feature,omitempty"`
	FeatureUpdate *UpdateFeature `json:"feature_update,omitempty"`
	FeatureLink   *LinkFeatures  `json:"feature_link,omitempty"`
}

// Result is what applying an operation produced.
type Result struct {
	Key           string `json:"key,omitempty"`          // created or updated item or feature
	DependencyID  string `json:"dependency,omitempty"`   // added dependency
	FeatureLinkID string `json:"feature_link,omitempty"` // added feature link
}

// Changeset is a batch of proposed planning changes for one project.
type Changeset struct {
	ID         string
	ProjectID  string
	Title      string
	Summary    string // Markdown: why these changes
	Ops        []Op
	Status     Status
	ProposedBy event.Actor
	CreatedAt  time.Time
	DecidedBy  event.Actor
	DecidedAt  time.Time
	Approved   []int    // indices of applied operations
	Results    []Result // per operation; zero for operations not applied
	Version    int64
}

// Limits.
const (
	MaxOps     = 200
	maxTitle   = 300
	maxSummary = 50000
)

var refRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var opKinds = []OpKind{OpCreateItem, OpUpdateItem, OpAddDependency, OpCreateFeature, OpUpdateFeature, OpLinkFeatures}

// IsRef reports whether reference s points into the changeset, and returns
// the referenced Ref.
func IsRef(s string) (string, bool) {
	return strings.CutPrefix(s, "$")
}

// Validate checks the changeset's own consistency: operation shapes, item
// fields, and that "$ref" references point to earlier create_item
// operations of a suitable kind. References to existing items are checked
// by the application against the project.
func (c Changeset) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Title) == "" || utf8.RuneCountInString(c.Title) > maxTitle {
		errs = append(errs, fmt.Errorf("title must be 1-%d characters", maxTitle))
	}
	if utf8.RuneCountInString(c.Summary) > maxSummary {
		errs = append(errs, fmt.Errorf("summary must be at most %d characters", maxSummary))
	}
	if len(c.Ops) == 0 || len(c.Ops) > MaxOps {
		errs = append(errs, fmt.Errorf("a changeset needs 1-%d operations", MaxOps))
	}
	kinds := map[string]tracker.Kind{} // refs declared so far
	for i, op := range c.Ops {
		if err := validateOp(op, kinds); err != nil {
			errs = append(errs, fmt.Errorf("operation %d: %w", i+1, err))
		}
		if refRe.MatchString(op.Ref) {
			if _, dup := kinds[op.Ref]; !dup {
				switch {
				case op.Kind == OpCreateItem && op.Create != nil:
					kinds[op.Ref] = op.Create.Kind
				case op.Kind == OpCreateFeature && op.Feature != nil:
					kinds[op.Ref] = kindFeature
				}
			}
		}
	}
	return errors.Join(errs...)
}

func validateOp(op Op, kinds map[string]tracker.Kind) error {
	set := 0
	for _, p := range []bool{op.Create != nil, op.Update != nil, op.Dependency != nil, op.Feature != nil,
		op.FeatureUpdate != nil, op.FeatureLink != nil} {
		if p {
			set++
		}
	}
	switch {
	case op.Kind == OpCreateItem && op.Create != nil && set == 1:
		return validateCreate(op, kinds)
	case op.Kind == OpUpdateItem && op.Update != nil && set == 1:
		return validateUpdate(*op.Update, kinds)
	case op.Kind == OpAddDependency && op.Dependency != nil && set == 1:
		return validateDependency(*op.Dependency, kinds)
	case op.Kind == OpCreateFeature && op.Feature != nil && set == 1:
		return validateCreateFeature(op, kinds)
	case op.Kind == OpUpdateFeature && op.FeatureUpdate != nil && set == 1:
		return validateUpdateFeature(*op.FeatureUpdate)
	case op.Kind == OpLinkFeatures && op.FeatureLink != nil && set == 1:
		return validateLinkFeatures(*op.FeatureLink, kinds)
	case !slices.Contains(opKinds, op.Kind):
		return fmt.Errorf("unknown kind %q", op.Kind)
	default:
		return fmt.Errorf("%s needs exactly its own payload", op.Kind)
	}
}

func validateCreate(op Op, kinds map[string]tracker.Kind) error {
	cr := op.Create
	var errs []error
	if !refRe.MatchString(op.Ref) {
		errs = append(errs, fmt.Errorf("ref %q must be 1-64 lowercase letters, digits, '-' or '_'", op.Ref))
	} else if _, dup := kinds[op.Ref]; dup {
		errs = append(errs, fmt.Errorf("ref %q is declared twice", op.Ref))
	}
	it := tracker.Item{Kind: cr.Kind, Title: cr.Title, Description: cr.Description, State: tracker.InitialState(cr.Kind)}
	if cr.Kind == tracker.KindTicket {
		it.Type, it.AcceptanceCriteria, it.Policy = cr.Type, cr.AcceptanceCriteria, tracker.DefaultPolicy
		if it.Type == "" {
			it.Type = tracker.TypeFeature
		}
		if cr.Policy != nil {
			it.Policy = *cr.Policy
		}
	}
	if err := it.Validate(); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, featureRefs(cr.Features, cr.Kind == tracker.KindTicket, kinds))
	errs = append(errs, relation(cr.Epic, tracker.KindEpic, cr.Kind == tracker.KindTicket, kinds))
	errs = append(errs, relation(cr.Milestone, tracker.KindMilestone, cr.Kind != tracker.KindMilestone, kinds))
	return errors.Join(errs...)
}

func validateUpdate(up UpdateItem, kinds map[string]tracker.Kind) error {
	if _, ok := IsRef(up.Item); ok || strings.TrimSpace(up.Item) == "" {
		return errors.New("update_item needs the key of an existing item")
	}
	if up.Title == nil && up.Description == nil && up.Type == nil && up.AcceptanceCriteria == nil &&
		up.Policy == nil && up.Epic == nil && up.Milestone == nil && up.Features == nil {
		return errors.New("update_item changes nothing")
	}
	var errs []error
	if up.Title != nil && (strings.TrimSpace(*up.Title) == "" || utf8.RuneCountInString(*up.Title) > maxTitle) {
		errs = append(errs, fmt.Errorf("title must be 1-%d characters", maxTitle))
	}
	if up.Policy != nil {
		errs = append(errs, up.Policy.Validate())
	}
	// Whether the existing item may have an epic or milestone is checked
	// when applying, where its kind is known.
	if up.Epic != nil {
		errs = append(errs, relation(*up.Epic, tracker.KindEpic, true, kinds))
	}
	if up.Milestone != nil {
		errs = append(errs, relation(*up.Milestone, tracker.KindMilestone, true, kinds))
	}
	if up.Features != nil {
		// Whether the item is a ticket is checked when applying.
		errs = append(errs, featureRefs(*up.Features, true, kinds))
	}
	return errors.Join(errs...)
}

func validateCreateFeature(op Op, kinds map[string]tracker.Kind) error {
	var errs []error
	if !refRe.MatchString(op.Ref) {
		errs = append(errs, fmt.Errorf("ref %q must be 1-64 lowercase letters, digits, '-' or '_'", op.Ref))
	} else if _, dup := kinds[op.Ref]; dup {
		errs = append(errs, fmt.Errorf("ref %q is declared twice", op.Ref))
	}
	f := feature.Feature{Title: op.Feature.Title, Description: op.Feature.Description, Status: op.Feature.Status}
	if f.Status == "" {
		f.Status = feature.StatusPlanned
	}
	return errors.Join(append(errs, f.Validate())...)
}

func validateUpdateFeature(up UpdateFeature) error {
	if _, err := feature.ParseKey(up.Feature); err != nil {
		return fmt.Errorf("update_feature needs the key of an existing feature: %w", err)
	}
	if up.Title == nil && up.Description == nil && up.Status == nil && up.Projects == nil {
		return errors.New("update_feature changes nothing")
	}
	f := feature.Feature{Title: "x", Status: feature.StatusPlanned}
	if up.Title != nil {
		f.Title = *up.Title
	}
	if up.Description != nil {
		f.Description = *up.Description
	}
	if up.Status != nil {
		f.Status = *up.Status
	}
	return f.Validate()
}

func validateLinkFeatures(l LinkFeatures, kinds map[string]tracker.Kind) error {
	errs := []error{featureRef(l.From, kinds), featureRef(l.To, kinds)}
	if !slices.Contains(feature.LinkTypes, l.Type) {
		errs = append(errs, fmt.Errorf("link type %q must be one of %v", l.Type, feature.LinkTypes))
	}
	if l.From == l.To {
		errs = append(errs, errors.New("a feature cannot link to itself"))
	}
	return errors.Join(errs...)
}

// featureRefs checks a ticket's feature references.
func featureRefs(refs []string, allowed bool, kinds map[string]tracker.Kind) error {
	if len(refs) == 0 {
		return nil
	}
	if !allowed {
		return errors.New("only tickets change features")
	}
	var errs []error
	seen := map[string]bool{}
	for _, r := range refs {
		if seen[r] {
			errs = append(errs, fmt.Errorf("feature %s is listed twice", r))
		}
		seen[r] = true
		errs = append(errs, featureRef(r, kinds))
	}
	return errors.Join(errs...)
}

// featureRef checks a feature key or a "$ref" to an earlier create_feature.
func featureRef(ref string, kinds map[string]tracker.Kind) error {
	if r, ok := IsRef(ref); ok {
		if kinds[r] != kindFeature {
			return fmt.Errorf("%s does not name an earlier create_feature operation", ref)
		}
		return nil
	}
	_, err := feature.ParseKey(ref)
	return err
}

func validateDependency(d AddDependency, kinds map[string]tracker.Kind) error {
	if d.Type != tracker.DepBlocks && d.Type != tracker.DepRelates {
		return fmt.Errorf("dependency type %q must be blocks or relates", d.Type)
	}
	if d.From == d.To {
		return tracker.ErrSelfDependency
	}
	return errors.Join(reference(d.From, kinds), reference(d.To, kinds))
}

// relation checks an epic or milestone reference of kind want.
func relation(ref string, want tracker.Kind, allowed bool, kinds map[string]tracker.Kind) error {
	if ref == "" {
		return nil
	}
	if !allowed {
		return fmt.Errorf("this item cannot have a %s", want)
	}
	if err := reference(ref, kinds); err != nil {
		return err
	}
	if r, ok := IsRef(ref); ok && kinds[r] != want {
		return fmt.Errorf("%s is a %s, not a %s", ref, kinds[r], want)
	}
	return nil
}

// reference checks that ref is non-empty and, when it points into the
// changeset, names an earlier create_item operation.
func reference(ref string, kinds map[string]tracker.Kind) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("empty item reference")
	}
	if r, ok := IsRef(ref); ok {
		if _, declared := kinds[r]; !declared {
			return fmt.Errorf("%s does not name an earlier create_item operation", ref)
		}
	}
	return nil
}

// CheckApproval checks a set of approved operation indices (0-based): at
// least one, each in range and listed once, and every "$ref" an approved
// operation uses must point to an approved create_item operation.
func (c Changeset) CheckApproval(approved []int) error {
	if len(approved) == 0 {
		return errors.New("approve at least one operation")
	}
	ok := map[int]bool{}
	for _, i := range approved {
		if i < 0 || i >= len(c.Ops) {
			return fmt.Errorf("operation %d does not exist", i+1)
		}
		if ok[i] {
			return fmt.Errorf("operation %d is approved twice", i+1)
		}
		ok[i] = true
	}
	declared := map[string]int{}
	for i, op := range c.Ops {
		if op.Kind == OpCreateItem || op.Kind == OpCreateFeature {
			declared[op.Ref] = i
		}
	}
	for _, i := range approved {
		for _, ref := range c.Ops[i].references() {
			if r, isRef := IsRef(ref); isRef && !ok[declared[r]] {
				return fmt.Errorf("operation %d needs operation %d (%s), which is not approved", i+1, declared[r]+1, ref)
			}
		}
	}
	return nil
}

// references lists the item references an operation uses.
func (op Op) references() []string {
	var out []string
	switch {
	case op.Create != nil:
		out = append(append(out, op.Create.Epic, op.Create.Milestone), op.Create.Features...)
	case op.Update != nil:
		if op.Update.Epic != nil {
			out = append(out, *op.Update.Epic)
		}
		if op.Update.Milestone != nil {
			out = append(out, *op.Update.Milestone)
		}
		if op.Update.Features != nil {
			out = append(out, *op.Update.Features...)
		}
	case op.Dependency != nil:
		out = append(out, op.Dependency.From, op.Dependency.To)
	case op.FeatureLink != nil:
		out = append(out, op.FeatureLink.From, op.FeatureLink.To)
	}
	return out
}
