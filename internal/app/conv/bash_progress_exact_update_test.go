package conv

import (
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestExactProgressTypedUpdate(t *testing.T) {
	m := &Model{}
	m.Tool.Track([]core.ToolCall{{ID: "first", Name: "Bash"}, {ID: "next", Name: "Bash"}})
	applyAgentEvent(nil, m, sdkagent.ToolUpdate{ID: "first", Name: "Bash"})
	started := time.Now().Add(-time.Minute)
	m.Tool.StartedAt["first"] = started
	update := sdkagent.ToolUpdate{ID: "first", Name: "Bash", Partial: sdkagent.Result{Content: ai.TextContent("1.2k lines"), Details: toolresult.BashDetails{LineCount: 1234, Tail: "last", TailTruncated: true}}}
	applyAgentEvent(nil, m, update)
	got := m.Tool.Progress["first"]
	if got.LineCount != 1234 || !got.TailTruncated || got.Counter != "1.2k lines" || got.Tail != "last" {
		t.Fatalf("progress=%+v", got)
	}
	if !m.Tool.StartedAt["first"].Equal(started) {
		t.Fatal("progress reset start time")
	}
	m.Tool.MarkComplete("first")
	delete(m.Tool.Progress, "first")
	applyAgentEvent(nil, m, update)
	if _, ok := m.Tool.Progress["first"]; ok {
		t.Fatal("completed report accepted")
	}
	if m.Tool.CurrentIdx != 1 {
		t.Fatal("late progress changed current call")
	}
	m.Tool.Track([]core.ToolCall{{ID: "successor", Name: "Bash"}})
	applyAgentEvent(nil, m, update)
	if len(m.Tool.Progress) != 0 || len(m.Tool.StartedAt) != 0 {
		t.Fatal("foreign batch report changed successor")
	}
	update.ID = "successor"
	update.Name = "Read"
	applyAgentEvent(nil, m, update)
	if len(m.Tool.Progress) != 0 || len(m.Tool.StartedAt) != 0 {
		t.Fatal("non-shell typed report changed successor")
	}
}
