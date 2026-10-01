// Package tracker defines Ballet's planning entities: milestones, epics and
// tickets ("items"). All items of a project share one key sequence
// (ACME-1, ACME-2, …), so dependencies can connect any two of them.
package tracker

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind is the kind of item.
type Kind string

// Item kinds.
const (
	KindMilestone Kind = "milestone"
	KindEpic      Kind = "epic"
	KindTicket    Kind = "ticket"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindMilestone || k == KindEpic || k == KindTicket }

// TicketType classifies tickets.
type TicketType string

// Ticket types.
const (
	TypeFeature  TicketType = "feature"
	TypeBug      TicketType = "bug"
	TypeTechDebt TicketType = "tech_debt"
	TypeDocs     TicketType = "docs"
	TypeSpike    TicketType = "spike"
)

var ticketTypes = []TicketType{TypeFeature, TypeBug, TypeTechDebt, TypeDocs, TypeSpike}

// State is an item's lifecycle state.
type State string

// States. Milestones and epics use open, done and cancelled; tickets use
// the rest. A ticket in progress additionally records its pipeline stage.
const (
	StateOpen             State = "open"
	StateBacklog          State = "backlog"
	StateReady            State = "ready"
	StateInProgress       State = "in_progress"
	StateWaitingForAnswer State = "waiting_for_answer"
	StatePaused           State = "paused"
	StateDone             State = "done"
	StateCancelled        State = "cancelled"
)

// ReviewMode says who reviews a ticket's pull request (ADR-0008).
type ReviewMode string

// Review modes.
const (
	ReviewAgent      ReviewMode = "agent"
	ReviewAgentHuman ReviewMode = "agent+human"
)

// MergeMode says who merges a ticket's pull request (ADR-0008).
type MergeMode string

// Merge modes.
const (
	MergeAuto   MergeMode = "auto"
	MergeManual MergeMode = "manual"
)

// Policy is a ticket's execution policy.
type Policy struct {
	ReviewMode ReviewMode `json:"review_mode"`
	MergeMode  MergeMode  `json:"merge_mode"`
}

// DefaultPolicy is fully autonomous (ADR-0008).
var DefaultPolicy = Policy{ReviewMode: ReviewAgent, MergeMode: MergeAuto}

// Validate checks the policy values.
func (p Policy) Validate() error {
	if p.ReviewMode != ReviewAgent && p.ReviewMode != ReviewAgentHuman {
		return fmt.Errorf("review_mode %q must be agent or agent+human", p.ReviewMode)
	}
	if p.MergeMode != MergeAuto && p.MergeMode != MergeManual {
		return fmt.Errorf("merge_mode %q must be auto or manual", p.MergeMode)
	}
	return nil
}

// Item is a milestone, epic or ticket.
type Item struct {
	ID          string
	ProjectID   string
	Number      int64  // per-project sequence number
	Key         string // "<PROJECT>-<number>"
	Kind        Kind
	Title       string
	Description string
	State       State
	Stage       string // current pipeline stage of a ticket in progress

	// Tickets only.
	Type               TicketType
	AcceptanceCriteria []string
	Policy             Policy
	EpicID             string

	// Tickets and epics.
	MilestoneID string

	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// InitialState is the state a new item of kind k starts in.
func InitialState(k Kind) State {
	if k == KindTicket {
		return StateBacklog
	}
	return StateOpen
}

// Limits on item content.
const (
	maxTitle     = 300
	maxDesc      = 50000
	maxCriteria  = 50
	maxCriterion = 2000
)

// Validate checks an item's own fields. Relations (epic, milestone) are
// checked by the caller, which knows the related items.
func (it Item) Validate() error {
	var errs []error
	if !it.Kind.Valid() {
		errs = append(errs, fmt.Errorf("unknown kind %q", it.Kind))
	}
	if strings.TrimSpace(it.Title) == "" {
		errs = append(errs, errors.New("title must not be empty"))
	}
	if utf8.RuneCountInString(it.Title) > maxTitle {
		errs = append(errs, fmt.Errorf("title must be at most %d characters", maxTitle))
	}
	if utf8.RuneCountInString(it.Description) > maxDesc {
		errs = append(errs, fmt.Errorf("description must be at most %d characters", maxDesc))
	}
	if it.Kind == KindTicket {
		if !slices.Contains(ticketTypes, it.Type) {
			errs = append(errs, fmt.Errorf("ticket type %q must be one of feature, bug, tech_debt, docs, spike", it.Type))
		}
		if err := it.Policy.Validate(); err != nil {
			errs = append(errs, err)
		}
		if len(it.AcceptanceCriteria) > maxCriteria {
			errs = append(errs, fmt.Errorf("at most %d acceptance criteria", maxCriteria))
		}
		for _, c := range it.AcceptanceCriteria {
			if strings.TrimSpace(c) == "" || utf8.RuneCountInString(c) > maxCriterion {
				errs = append(errs, fmt.Errorf("acceptance criteria must be non-empty and at most %d characters", maxCriterion))
				break
			}
		}
	} else {
		if it.Type != "" || len(it.AcceptanceCriteria) > 0 || it.EpicID != "" {
			errs = append(errs, fmt.Errorf("%s items have no type, acceptance criteria or epic", it.Kind))
		}
		if it.Kind == KindMilestone && it.MilestoneID != "" {
			errs = append(errs, errors.New("milestones cannot belong to a milestone"))
		}
	}
	return errors.Join(errs...)
}

// humanTransitions lists the states a human may move an item to, by kind
// and current state. The orchestrator owns in_progress and
// waiting_for_answer.
var humanTransitions = map[Kind]map[State][]State{
	KindTicket: {
		StateBacklog:          {StateReady, StateCancelled, StateDone},
		StateReady:            {StateBacklog, StatePaused, StateCancelled, StateDone},
		StateInProgress:       {StatePaused, StateCancelled},
		StateWaitingForAnswer: {StatePaused, StateCancelled},
		StatePaused:           {StateReady, StateBacklog, StateCancelled, StateDone},
		StateDone:             {StateBacklog},
		StateCancelled:        {StateBacklog},
	},
	KindEpic: {
		StateOpen:      {StateDone, StateCancelled},
		StateDone:      {StateOpen},
		StateCancelled: {StateOpen},
	},
}

func init() { humanTransitions[KindMilestone] = humanTransitions[KindEpic] }

// CanTransition reports whether a human may move an item of kind k from
// one state to another.
func CanTransition(k Kind, from, to State) bool {
	return slices.Contains(humanTransitions[k][from], to)
}

// Terminal reports whether s ends an item's lifecycle (it may be reopened).
func Terminal(s State) bool { return s == StateDone || s == StateCancelled }
