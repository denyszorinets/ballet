package planner_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denyszorinets/ballet/core/internal/domain/planner"
)

func text(role planner.Role, s string) planner.Message {
	return planner.Message{Role: role, Content: []planner.Block{planner.Text(s)}}
}

func TestRepairHistory_ClosesInterruptedToolCalls(t *testing.T) {
	call := planner.Block{Type: planner.BlockToolUse, ToolUseID: "t1", Name: "list_items", Input: json.RawMessage(`{}`)}
	msgs := []planner.Message{
		text(planner.RoleUser, "plan auth"),
		{Role: planner.RoleAssistant, Content: []planner.Block{planner.Text("Let me look."), call}},
		// Core stopped before the tool result was stored.
		text(planner.RoleUser, "are you there?"),
	}
	got := planner.RepairHistory(msgs)
	assert.Len(t, got, 3)
	user := got[2]
	assert.Equal(t, planner.RoleUser, user.Role)
	assert.Equal(t, planner.BlockToolResult, user.Content[0].Type, "tool results come first")
	assert.Equal(t, "t1", user.Content[0].ToolUseID)
	assert.True(t, user.Content[0].IsError)
	assert.Equal(t, "are you there?", user.Content[1].Text)
}

func TestRepairHistory_MergesConsecutiveRolesAndDropsEmptyMessages(t *testing.T) {
	msgs := []planner.Message{
		text(planner.RoleUser, "a"),
		text(planner.RoleUser, "b"),
		{Role: planner.RoleAssistant},
		text(planner.RoleAssistant, "c"),
	}
	got := planner.RepairHistory(msgs)
	assert.Len(t, got, 2)
	assert.Len(t, got[0].Content, 2)
	assert.Equal(t, "c", got[1].Content[0].Text)
}

func TestRepairHistory_KeepsAnswersAndStartsWithUser(t *testing.T) {
	call := planner.Block{Type: planner.BlockToolUse, ToolUseID: "t1", Name: "x"}
	msgs := []planner.Message{
		text(planner.RoleAssistant, "orphan greeting"),
		text(planner.RoleUser, "go"),
		{Role: planner.RoleAssistant, Content: []planner.Block{call}},
		{Role: planner.RoleUser, Content: []planner.Block{{Type: planner.BlockToolResult, ToolUseID: "t1", Text: "ok"}}},
	}
	got := planner.RepairHistory(msgs)
	assert.Equal(t, planner.RoleUser, got[0].Role)
	assert.Len(t, got, 3)
	assert.False(t, got[2].Content[0].IsError)
}

func TestToolCalls(t *testing.T) {
	m := planner.Message{Role: planner.RoleAssistant, Content: []planner.Block{
		planner.Text("x"), {Type: planner.BlockToolUse, ToolUseID: "a"}, {Type: planner.BlockToolUse, ToolUseID: "b"},
	}}
	assert.Len(t, m.ToolCalls(), 2)
}

func TestValidateTitle(t *testing.T) {
	assert.NoError(t, planner.ValidateTitle("Auth planning"))
	assert.Error(t, planner.ValidateTitle(" "))
}
