package conv

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

type resultFenceRuntime struct {
	postToolRuntime
	outbox  chan core.Event
	results int
}

func (r *resultFenceRuntime) AgentOutbox() <-chan core.Event { return r.outbox }
func (r *resultFenceRuntime) OnToolResult(tr core.ToolResult) *core.ToolResult {
	r.results++
	return &tr
}

func TestToolResultFencesBeforeSideEffects(t *testing.T) {
	t.Run("replaced outbox", func(t *testing.T) {
		m := NewModel(80)
		rt := &resultFenceRuntime{outbox: make(chan core.Event)}
		old := make(chan core.Event)
		_, handled := Update(rt, &m, AgentOutboxMsg{Outbox: old, Event: sdkagent.ToolEnd{ID: "call", Name: "Bash"}})
		if !handled || rt.results != 0 || len(m.Messages) != 0 || rt.drainCalls != 0 {
			t.Fatal("retired result reached workspace policy or queue")
		}
	})
	t.Run("cancelled result already present", func(t *testing.T) {
		m := NewModel(80)
		rt := &resultFenceRuntime{}
		m.Tool.Track([]core.ToolCall{{ID: "call", Name: "Bash"}})
		m.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "call", ToolName: "Bash", IsError: true}})
		applyPostTool(rt, &m, core.ToolResult{ToolCallID: "call", ToolName: "Bash"}, nil)
		if rt.results != 0 || len(m.Messages) != 1 || rt.drainCalls != 0 {
			t.Fatal("buffered completion reached workspace policy or queue")
		}
	})
}
