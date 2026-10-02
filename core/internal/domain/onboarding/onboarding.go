// Package onboarding builds the onboarding bundle of an agent run: the
// ticket, its place in the plan, what it depends on and the knowledge
// about it, rendered as the session's prompt within a size limit.
package onboarding

import (
	"fmt"
	"strings"
)

// Item is a tracker item as the bundle shows it.
type Item struct {
	Key, Kind, Title, State string
	Description             string
}

// Dependency is an item the ticket depends on or relates to.
type Dependency struct {
	Item
	Relation string // "blocked_by", "blocks", "relates"
	Report   string // the latest result of its runs, if any
}

// Knowledge is a knowledge entry relevant to the ticket.
type Knowledge struct {
	ID, Kind, Title, Body string
	Linked                bool // linked to the ticket (else found by search)
}

// Bundle is everything a session starts with.
type Bundle struct {
	Project            string
	Stage              string
	StageInstructions  string
	Ticket             Item
	Type               string
	AcceptanceCriteria []string
	ReviewMode         string
	MergeMode          string
	Branch             string
	Epic, Milestone    *Item
	Dependencies       []Dependency
	Knowledge          []Knowledge
	Extra              string // additional instructions from whoever queued the run
}

// DefaultMaxBytes bounds a rendered bundle.
const DefaultMaxBytes = 60_000

// Render renders the bundle as Markdown of at most maxBytes. The ticket
// and instructions always fit (descriptions are cut first); dependencies
// and knowledge are shortened, then dropped, from the end.
func (b Bundle) Render(maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	head := b.head()
	tail := b.tail()
	// Optional sections, most important first.
	var sections []string
	if b.Epic != nil || b.Milestone != nil {
		sections = append(sections, b.plan())
	}
	for _, d := range b.Dependencies {
		sections = append(sections, dependency(d))
	}
	for _, k := range b.Knowledge {
		sections = append(sections, knowledge(k))
	}
	// Room for the note on omitted sections.
	const noteReserve = 128
	budget := maxBytes - len(head) - len(tail) - noteReserve
	if budget < 0 && len(head)+len(tail) <= maxBytes {
		budget = 0
	}
	if budget < 0 {
		// Even the essentials are too long: cut the ticket description.
		return truncate(head, maxBytes-len(tail)-1) + "\n" + tail
	}
	var out strings.Builder
	out.WriteString(head)
	omitted := 0
	for _, s := range sections {
		switch {
		case len(s) <= budget:
			out.WriteString(s)
			budget -= len(s)
		case budget > 400:
			cut := truncate(s, budget-2) + "\n\n"
			out.WriteString(cut)
			budget -= len(cut)
		default:
			omitted++
		}
	}
	if omitted > 0 {
		out.WriteString(fmt.Sprintf("_%d more section(s) omitted for size; use the knowledge and tracker tools to look further._\n\n", omitted))
	}
	out.WriteString(tail)
	return out.String()
}

func (b Bundle) head() string {
	var s strings.Builder
	fmt.Fprintf(&s, "# %s: %s\n\n", b.Ticket.Key, b.Ticket.Title)
	fmt.Fprintf(&s, "You are working on ticket **%s** of project **%s**, in the **%s** stage.\n", b.Ticket.Key, b.Project, b.Stage)
	if b.Branch != "" {
		fmt.Fprintf(&s, "The repository is checked out on branch `%s`; commit and push your work there.\n", b.Branch)
	}
	s.WriteString("\n")
	if b.StageInstructions != "" {
		s.WriteString("## Your task in this stage\n\n" + strings.TrimSpace(b.StageInstructions) + "\n\n")
	}
	s.WriteString("## Ticket\n\n")
	fmt.Fprintf(&s, "- Type: %s\n- State: %s\n", or(b.Type, "feature"), b.Ticket.State)
	if b.ReviewMode != "" {
		fmt.Fprintf(&s, "- Review: %s; merge: %s\n", b.ReviewMode, b.MergeMode)
	}
	s.WriteString("\n")
	if d := strings.TrimSpace(b.Ticket.Description); d != "" {
		s.WriteString(d + "\n\n")
	}
	if len(b.AcceptanceCriteria) > 0 {
		s.WriteString("### Acceptance criteria\n\n")
		for _, c := range b.AcceptanceCriteria {
			s.WriteString("- [ ] " + c + "\n")
		}
		s.WriteString("\n")
	}
	return s.String()
}

func (b Bundle) tail() string {
	if strings.TrimSpace(b.Extra) == "" {
		return ""
	}
	return "## Additional instructions\n\n" + strings.TrimSpace(b.Extra) + "\n"
}

func (b Bundle) plan() string {
	var s strings.Builder
	s.WriteString("## Place in the plan\n\n")
	for _, it := range []*Item{b.Milestone, b.Epic} {
		if it == nil {
			continue
		}
		fmt.Fprintf(&s, "### %s %s: %s\n\n", strings.ToUpper(it.Kind[:1])+it.Kind[1:], it.Key, it.Title)
		if d := strings.TrimSpace(it.Description); d != "" {
			s.WriteString(d + "\n\n")
		}
	}
	return s.String()
}

var relations = map[string]string{"blocked_by": "Depends on", "blocks": "Blocks", "relates": "Related to"}

func dependency(d Dependency) string {
	var s strings.Builder
	fmt.Fprintf(&s, "## %s %s: %s (%s)\n\n", or(relations[d.Relation], "Related to"), d.Key, d.Title, d.State)
	if desc := strings.TrimSpace(d.Description); desc != "" {
		s.WriteString(desc + "\n\n")
	}
	if r := strings.TrimSpace(d.Report); r != "" {
		s.WriteString("Latest report:\n\n> " + strings.ReplaceAll(r, "\n", "\n> ") + "\n\n")
	}
	return s.String()
}

func knowledge(k Knowledge) string {
	how := "found by search"
	if k.Linked {
		how = "linked to this ticket"
	}
	return fmt.Sprintf("## Knowledge (%s, %s): %s\n\n_Entry %s._\n\n%s\n\n", k.Kind, how, k.Title, k.ID, strings.TrimSpace(k.Body))
}

// truncate cuts s to at most n bytes, marking the cut with " …".
func truncate(s string, n int) string {
	const mark = " …"
	if len(s) <= n {
		return s
	}
	if n <= len(mark) {
		return ""
	}
	cut := s[:n-len(mark)]
	// Do not split a UTF-8 sequence.
	for len(cut) > 0 && cut[len(cut)-1]&0xC0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	if len(cut) > 0 && cut[len(cut)-1] >= 0xC0 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimRight(cut, " \n") + mark
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// DefaultStageInstructions are the instructions of the default pipeline's
// stages; projects shape the details through their skills.
func DefaultStageInstructions(stage string) string {
	switch stage {
	case "implement":
		return "Implement the ticket so that every acceptance criterion holds. Add or update tests and " +
			"documentation as this project's skills require. Commit in small steps with clear messages and push."
	case "review":
		return "Review the changes on this branch against the ticket and its acceptance criteria: correctness, " +
			"tests, documentation, maintainability. Report concrete findings; do not rewrite the implementation."
	case "verify":
		return "Verify that the acceptance criteria hold: build, run the tests and exercise the behaviour. " +
			"Report what you checked and what failed."
	case "integrate":
		return "Bring the branch up to date with the default branch, resolve conflicts, and make sure the " +
			"checks pass. Push the result."
	}
	return fmt.Sprintf("Carry out the %q stage for this ticket.", stage)
}
