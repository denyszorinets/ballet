package skill

import (
	"fmt"
	"sort"
)

// Pin fixes how a project uses a skill name.
type Pin struct {
	Name     string
	Version  int64 // 0: latest published
	Disabled bool  // exclude the skill from the project
}

// Resolved is one skill in a project's effective set.
type Resolved struct {
	Name    string
	Skill   Skill // the most specific skill with this name
	Version int64 // the version to use
	Pinned  bool
	Problem string // non-empty: the skill cannot be used as configured
}

var specificity = map[ScopeKind]int{ScopeOrganization: 0, ScopeCustomer: 1, ScopeProject: 2}

// Resolve computes a project's effective skills from the skills of its
// scope chain (organization, its customer, the project) and its pins: the
// most specific scope wins per name; unpublished skills are skipped;
// disabled pins remove a skill; version pins must name an existing
// version (otherwise Problem is set and Version is 0).
func Resolve(skills []Skill, pins []Pin) []Resolved {
	best := map[string]Skill{}
	for _, s := range skills {
		if cur, ok := best[s.Name]; !ok || specificity[s.Scope.Kind] > specificity[cur.Scope.Kind] {
			best[s.Name] = s
		}
	}
	pinned := map[string]Pin{}
	for _, p := range pins {
		pinned[p.Name] = p
	}
	var out []Resolved
	for name, s := range best {
		p, isPinned := pinned[name]
		if isPinned && p.Disabled {
			continue
		}
		r := Resolved{Name: name, Skill: s, Version: s.LatestVersion, Pinned: isPinned && p.Version > 0}
		switch {
		case r.Pinned && p.Version > s.LatestVersion:
			r.Version, r.Problem = 0, fmt.Sprintf("pinned to version %d, but the latest published version is %d", p.Version, s.LatestVersion)
		case r.Pinned:
			r.Version = p.Version
		case s.LatestVersion == 0:
			continue // never published: not part of any project yet
		}
		out = append(out, r)
	}
	for name, p := range pinned {
		if _, ok := best[name]; !ok && !p.Disabled {
			out = append(out, Resolved{Name: name, Pinned: true, Problem: "no skill with this name is visible to the project"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
