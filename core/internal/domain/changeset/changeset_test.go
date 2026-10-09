package changeset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/feature"
	"github.com/denyszorinets/ballet/core/internal/domain/tracker"
)

func create(ref string, kind tracker.Kind, title string) changeset.Op {
	return changeset.Op{Kind: changeset.OpCreateItem, Ref: ref, Create: &changeset.CreateItem{Kind: kind, Title: title}}
}

func dep(from, to string) changeset.Op {
	return changeset.Op{Kind: changeset.OpAddDependency, Dependency: &changeset.AddDependency{From: from, To: to, Type: tracker.DepBlocks}}
}

func plan() changeset.Changeset {
	epic := create("auth", tracker.KindEpic, "Auth")
	login := create("login", tracker.KindTicket, "Login")
	login.Create.Epic = "$auth"
	logout := create("logout", tracker.KindTicket, "Logout")
	logout.Create.Epic = "$auth"
	title := "Renamed"
	return changeset.Changeset{Title: "Auth plan", Ops: []changeset.Op{
		epic, login, logout,
		dep("$login", "$logout"),
		{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: "WEB-1", Title: &title}},
		dep("WEB-1", "$login"),
	}}
}

func TestValidate_AcceptsAWellFormedPlan(t *testing.T) {
	require.NoError(t, plan().Validate())
}

func TestValidate_RejectsMalformedOperations(t *testing.T) {
	cases := map[string]func(*changeset.Changeset){
		"no title":            func(c *changeset.Changeset) { c.Title = " " },
		"no operations":       func(c *changeset.Changeset) { c.Ops = nil },
		"duplicate ref":       func(c *changeset.Changeset) { c.Ops[1].Ref = "auth" },
		"bad ref":             func(c *changeset.Changeset) { c.Ops[0].Ref = "Has Space" },
		"missing ref":         func(c *changeset.Changeset) { c.Ops[0].Ref = "" },
		"forward reference":   func(c *changeset.Changeset) { c.Ops[0], c.Ops[1] = c.Ops[1], c.Ops[0] },
		"unknown reference":   func(c *changeset.Changeset) { c.Ops[3].Dependency.From = "$nope" },
		"epic ref not epic":   func(c *changeset.Changeset) { c.Ops[2].Create.Epic = "$login" },
		"empty title":         func(c *changeset.Changeset) { c.Ops[1].Create.Title = "" },
		"unknown kind":        func(c *changeset.Changeset) { c.Ops[0].Kind = "delete_item" },
		"payload mismatch":    func(c *changeset.Changeset) { c.Ops[0].Create = nil },
		"empty update":        func(c *changeset.Changeset) { c.Ops[4].Update.Title = nil },
		"update new item":     func(c *changeset.Changeset) { c.Ops[4].Update.Item = "$login" },
		"self dependency":     func(c *changeset.Changeset) { c.Ops[3].Dependency.To = "$login" },
		"bad dependency type": func(c *changeset.Changeset) { c.Ops[3].Dependency.Type = "follows" },
		"epic on milestone": func(c *changeset.Changeset) {
			c.Ops[0] = create("auth", tracker.KindMilestone, "M")
			c.Ops[0].Create.Epic = "WEB-2"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := plan()
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
}

func TestCheckApproval_RequiresTheOperationsApprovedOnesDependOn(t *testing.T) {
	c := plan()
	require.NoError(t, c.CheckApproval([]int{0, 1, 2, 3, 4, 5}))
	require.NoError(t, c.CheckApproval([]int{0, 1}))
	require.NoError(t, c.CheckApproval([]int{4}))

	err := c.CheckApproval([]int{1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation 2 needs operation 1")
	assert.Error(t, c.CheckApproval([]int{0, 1, 3}), "the dependency needs the logout ticket")
	assert.Error(t, c.CheckApproval(nil), "nothing approved")
	assert.Error(t, c.CheckApproval([]int{9}))
	assert.Error(t, c.CheckApproval([]int{0, 0}))
}

func TestIsRef(t *testing.T) {
	ref, ok := changeset.IsRef("$login")
	assert.True(t, ok)
	assert.Equal(t, "login", ref)
	_, ok = changeset.IsRef("WEB-1")
	assert.False(t, ok)
}

func featurePlan() changeset.Changeset {
	desc := "Exports invoices as CSV and PDF."
	return changeset.Changeset{Title: "Invoice export", Ops: []changeset.Op{
		{Kind: changeset.OpCreateFeature, Ref: "export", Feature: &changeset.CreateFeature{Title: "Invoice export", Projects: []string{"WEB"}}},
		{Kind: changeset.OpUpdateFeature, FeatureUpdate: &changeset.UpdateFeature{Feature: "F-1", Description: &desc}},
		{Kind: changeset.OpLinkFeatures, FeatureLink: &changeset.LinkFeatures{From: "$export", To: "F-1", Type: feature.LinkDerivedFrom}},
		{Kind: changeset.OpCreateItem, Ref: "csv", Create: &changeset.CreateItem{Kind: tracker.KindTicket, Title: "CSV", Features: []string{"$export", "F-1"}}},
		{Kind: changeset.OpUpdateItem, Update: &changeset.UpdateItem{Item: "WEB-3", Features: &[]string{"$export"}}},
	}}
}

func TestValidate_FeatureOperations(t *testing.T) {
	require.NoError(t, featurePlan().Validate())
	cases := map[string]func(*changeset.Changeset){
		"feature without title":     func(c *changeset.Changeset) { c.Ops[0].Feature.Title = "" },
		"bad feature status":        func(c *changeset.Changeset) { c.Ops[0].Feature.Status = "shipped" },
		"update needs a key":        func(c *changeset.Changeset) { c.Ops[1].FeatureUpdate.Feature = "$export" },
		"update with no change":     func(c *changeset.Changeset) { c.Ops[1].FeatureUpdate.Description = nil },
		"bad feature key":           func(c *changeset.Changeset) { c.Ops[1].FeatureUpdate.Feature = "WEB-1" },
		"link type":                 func(c *changeset.Changeset) { c.Ops[2].FeatureLink.Type = "parent" },
		"link to itself":            func(c *changeset.Changeset) { c.Ops[2].FeatureLink.To = "$export" },
		"link to a ticket ref":      func(c *changeset.Changeset) { c.Ops[2].FeatureLink.To = "$csv" },
		"ticket feature not a ref":  func(c *changeset.Changeset) { c.Ops[3].Create.Features = []string{"WEB-1"} },
		"features on an epic":       func(c *changeset.Changeset) { c.Ops[3].Create.Kind = tracker.KindEpic },
		"two payloads":              func(c *changeset.Changeset) { c.Ops[0].Create = &changeset.CreateItem{} },
		"unknown feature reference": func(c *changeset.Changeset) { c.Ops[3].Create.Features = []string{"$nope"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := featurePlan()
			mutate(&c)
			assert.Error(t, c.Validate())
		})
	}
}

func TestCheckApproval_TicketsNeedTheirNewFeatures(t *testing.T) {
	c := featurePlan()
	require.NoError(t, c.CheckApproval([]int{0, 3}))
	require.NoError(t, c.CheckApproval([]int{1}))
	assert.Error(t, c.CheckApproval([]int{3}), "the ticket needs the new feature")
	assert.Error(t, c.CheckApproval([]int{2}), "the link needs the new feature")
	assert.Error(t, c.CheckApproval([]int{4}), "the update needs the new feature")
}
