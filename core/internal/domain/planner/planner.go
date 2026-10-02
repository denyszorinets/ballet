// Package planner models planner chat sessions (ADR-0020): a conversation
// between a human and the planner agent, persisted as a transcript of
// messages made of text, tool calls and tool results.
package planner

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Session is one planner conversation in a project.
type Session struct {
	ID        string
	ProjectID string
	Title     string
	CreatedBy string // subject of the human who started it
	CreatedAt time.Time
	UpdatedAt time.Time
	// Summary replaces messages up to SummaryUpTo (a Seq) in what the model
	// sees once the conversation was compacted; "" when never compacted.
	Summary     string
	SummaryUpTo int64
	// QuestionID is set for a question's sub-chat; Context then holds what
	// the planner knows about the question.
	QuestionID string
	Context    string
}

const maxTitle = 200

// ValidateTitle checks a session title.
func ValidateTitle(t string) error {
	if strings.TrimSpace(t) == "" || utf8.RuneCountInString(t) > maxTitle {
		return errors.New("title must be 1-200 characters")
	}
	return nil
}

// Role is who wrote a message, in the LLM's terms. Tool results are user
// messages.
type Role string

// Roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType is the type of a content block.
type BlockType string

// Block types.
const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block is one piece of a message.
type Block struct {
	Type      BlockType       `json:"type"`
	Text      string          `json:"text,omitempty"`        // text; tool_result content
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_use, tool_result
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
}

// Text is a text block.
func Text(s string) Block { return Block{Type: BlockText, Text: s} }

// Usage is the token usage of one LLM response.
type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// Message is one transcript entry.
type Message struct {
	SessionID  string
	Seq        int64 // position in the session, from 1
	Role       Role
	Content    []Block
	Author     string // human subject for user text; empty otherwise
	StopReason string // assistant: why the model stopped
	Usage      Usage  // assistant
	CreatedAt  time.Time
}

// ToolCalls returns the tool_use blocks of a message.
func (m Message) ToolCalls() []Block {
	var out []Block
	for _, b := range m.Content {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

// interrupted is the result recorded for a tool call that never finished
// (Core stopped or the turn was cancelled).
const interrupted = "The tool call was interrupted and did not complete."

// RepairHistory turns a stored transcript into a valid conversation for
// the model: it starts with a user message, roles alternate (consecutive
// messages of one role are merged, empty ones dropped), and every tool call
// is answered by a tool result in the following user message (calls left
// unanswered by an interruption get an error result).
func RepairHistory(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if len(m.Content) == 0 {
			continue
		}
		if len(out) == 0 && m.Role != RoleUser {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			merged := out[n-1]
			merged.Content = append(append([]Block(nil), merged.Content...), m.Content...)
			out[n-1] = merged
			continue
		}
		out = append(out, m)
	}
	for i := range out {
		if out[i].Role != RoleAssistant {
			continue
		}
		calls := out[i].ToolCalls()
		if len(calls) == 0 {
			continue
		}
		if i+1 == len(out) {
			out = append(out, Message{Role: RoleUser})
		}
		answered := map[string]bool{}
		for _, b := range out[i+1].Content {
			if b.Type == BlockToolResult {
				answered[b.ToolUseID] = true
			}
		}
		var missing []Block
		for _, c := range calls {
			if !answered[c.ToolUseID] {
				missing = append(missing, Block{Type: BlockToolResult, ToolUseID: c.ToolUseID, Text: interrupted, IsError: true})
			}
		}
		if len(missing) > 0 {
			next := out[i+1]
			next.Content = append(missing, next.Content...)
			out[i+1] = next
		}
	}
	return out
}
