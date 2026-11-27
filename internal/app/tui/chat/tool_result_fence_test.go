package chat

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

type toolResultCounter struct {
	stepEndCounter
	outbox  chan core.Event
	results int
}

func (r *toolResultCounter) AgentOutbox() <-chan core.Event { return r.outbox }
func (r *toolResultCounter) OnToolResult(tr core.ToolResult) *core.ToolResult {
	r.results++
	return &tr
}

func TestToolResultFencesBeforeSideEffects(t *testing.T) {
	t.Run("replaced outbox", func(t *testing.T) {
		m := NewModel(80)
		counter := &toolResultCounter{outbox: make(chan core.Event)}
		old := make(chan core.Event)
		_, handled := Update(counter, &m, AgentOutboxMsg{Outbox: old, Event: sdkagent.ToolEnd{ID: "call", Name: "Bash"}})
		if !handled || counter.results != 0 || len(m.Messages) != 0 || counter.drainCalls != 0 {
			t.Fatal("retired result reached workspace policy or queue")
		}
	})
	t.Run("cancelled result already present", func(t *testing.T) {
		m := NewModel(80)
		counter := &toolResultCounter{}
		m.Tool.Track([]core.ToolCall{{ID: "call", Name: "Bash"}})
		m.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "call", ToolName: "Bash", IsError: true}})
		applyPostTool(counter, &m, core.ToolResult{ToolCallID: "call", ToolName: "Bash"}, nil)
		if counter.results != 0 || len(m.Messages) != 1 || counter.drainCalls != 0 {
			t.Fatal("buffered completion reached workspace policy or queue")
		}
	})
}
