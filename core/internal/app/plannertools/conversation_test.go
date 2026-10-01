package plannertools_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/denyszorinets/ballet/core/internal/app"
	"github.com/denyszorinets/ballet/core/internal/domain/changeset"
	"github.com/denyszorinets/ballet/core/internal/domain/planner"
	"github.com/denyszorinets/ballet/core/internal/infra/store"
)

// script answers with tool calls in order, then a final text.
type script struct {
	mu    sync.Mutex
	calls []planner.Block
}

func (s *script) Stream(_ context.Context, _ app.LLMRequest, onText func(string)) (app.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		onText("Proposed.")
		return app.LLMResponse{StopReason: "end_turn", Content: []planner.Block{planner.Text("Proposed.")}}, nil
	}
	c := s.calls[0]
	s.calls = s.calls[1:]
	return app.LLMResponse{StopReason: "tool_use", Content: []planner.Block{c}}, nil
}

func use(id, name, input string) planner.Block {
	return planner.Block{Type: planner.BlockToolUse, ToolUseID: id, Name: name, Input: json.RawMessage(input)}
}

func TestConversation_PlannerProposesAndTheHumanApproves(t *testing.T) {
	e := setup(t)
	bob := user(t, "bob", "devs")

	llm := &script{calls: []planner.Block{
		use("t1", "list_items", `{}`),
		use("t2", "search_knowledge", `{"query":"login"}`),
		use("t3", "propose_changeset", `{"title":"Login","operations":[
			{"kind":"create_item","ref":"login","create":{"kind":"ticket","title":"Login","acceptance_criteria":["OIDC"]}}]}`),
	}}
	tools := make([]app.PlannerTool, 0, len(e.tools))
	for _, tl := range e.tools {
		tools = append(tools, tl)
	}
	pl := &app.Planner{Store: e.cs.Store.(*store.Store), Tenancy: e.tracker.Tenancy, Authz: e.tracker.Authz, LLM: llm,
		Tools: tools, Now: time.Now, NewID: store.NewID, Context: t.Context()}
	s, err := pl.CreateSession(bob, "WEB", "Login")
	require.NoError(t, err)
	done := make(chan struct{})
	w, _, err := pl.Watch(bob, s.ID, func(o app.PlannerOutput) {
		if o.Type == app.OutputDone {
			close(done)
		}
	})
	require.NoError(t, err)
	defer w.Close()
	_, err = pl.Send(bob, s.ID, "Add login")
	require.NoError(t, err)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}

	_, msgs, err := pl.Session(bob, s.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 8)
	for _, i := range []int{2, 4, 6} {
		assert.False(t, msgs[i].Content[0].IsError, msgs[i].Content[0].Text)
	}
	list, err := e.cs.List(bob, "WEB", changeset.StatusProposed)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "planner:"+s.ID, list[0].ProposedBy.Subject)

	applied, err := e.cs.Apply(bob, list[0].ID, []int{0})
	require.NoError(t, err)
	assert.Equal(t, "WEB-1", applied.Results[0].Key)
}
