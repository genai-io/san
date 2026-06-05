package stream

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/llm"
)

func TestStateEmitsAndAccumulatesChunks(t *testing.T) {
	ch := make(chan llm.StreamChunk, 8)
	state := NewState("test")

	state.EmitText(context.Background(), ch, "hello")
	state.EmitThinking(context.Background(), ch, "thinking")
	state.EmitToolStart(context.Background(), ch, "tool-1", "Read")
	state.EmitToolInput(context.Background(), ch, "tool-1", `{"path":"a.go"}`)

	// Verify streaming chunks emitted in order
	msgs := []llm.StreamChunk{<-ch, <-ch, <-ch, <-ch}
	if msgs[0].Type != llm.ChunkTypeText || msgs[0].Text != "hello" {
		t.Fatalf("unexpected text chunk: %#v", msgs[0])
	}
	if msgs[1].Type != llm.ChunkTypeThinking || msgs[1].Text != "thinking" {
		t.Fatalf("unexpected thinking chunk: %#v", msgs[1])
	}
	if msgs[2].Type != llm.ChunkTypeToolStart || msgs[2].ToolID != "tool-1" || msgs[2].ToolName != "Read" {
		t.Fatalf("unexpected tool start chunk: %#v", msgs[2])
	}
	if msgs[3].Type != llm.ChunkTypeToolInput || msgs[3].ToolID != "tool-1" || msgs[3].Text != `{"path":"a.go"}` {
		t.Fatalf("unexpected tool input chunk: %#v", msgs[3])
	}

	// Content and thinking accumulate in internal buffers and flush on Finish.
	state.Finish(context.Background(), ch)
	doneChunk := <-ch
	if got := doneChunk.Response.Content; got != "hello" {
		t.Fatalf("expected content to accumulate, got %q", got)
	}
	if got := doneChunk.Response.Thinking; got != "thinking" {
		t.Fatalf("expected thinking to accumulate, got %q", got)
	}
}

func TestStateAddsToolCallsInStableOrder(t *testing.T) {
	byIndex := NewState("test")
	byIndex.AddToolCallsSorted(map[int]*core.ToolCall{
		2: {ID: "c", Name: "third"},
		0: {ID: "a", Name: "first"},
		1: {ID: "b", Name: "second"},
	})

	if len(byIndex.Response.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(byIndex.Response.ToolCalls))
	}
	if byIndex.Response.ToolCalls[0].ID != "a" || byIndex.Response.ToolCalls[1].ID != "b" || byIndex.Response.ToolCalls[2].ID != "c" {
		t.Fatalf("tool calls were not sorted by index: %#v", byIndex.Response.ToolCalls)
	}

	byKey := NewState("test")
	byKey.AddToolCallsByKey(map[string]*core.ToolCall{
		"z": {ID: "3", Name: "third"},
		"a": {ID: "1", Name: "first"},
		"m": {ID: "2", Name: "second"},
	})

	if len(byKey.Response.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(byKey.Response.ToolCalls))
	}
	if byKey.Response.ToolCalls[0].ID != "1" || byKey.Response.ToolCalls[1].ID != "2" || byKey.Response.ToolCalls[2].ID != "3" {
		t.Fatalf("tool calls were not sorted by key: %#v", byKey.Response.ToolCalls)
	}
}

func TestStateEnsureToolUseStopReason(t *testing.T) {
	state := NewState("test")
	state.Response.ToolCalls = []core.ToolCall{{ID: "tool-1", Name: "Read"}}
	state.EnsureToolUseStopReason()

	if got := state.Response.StopReason; got != "tool_use" {
		t.Fatalf("expected tool_use stop reason, got %q", got)
	}

	state.Response.StopReason = "max_tokens"
	state.EnsureToolUseStopReason()
	if got := state.Response.StopReason; got != "max_tokens" {
		t.Fatalf("expected existing stop reason to be preserved, got %q", got)
	}
}

func TestStateFailAndFinishEmitTerminalChunks(t *testing.T) {
	ch := make(chan llm.StreamChunk, 4)
	state := NewState("test")

	// Accumulate content via EmitText (content flushes in Finish)
	state.EmitText(context.Background(), ch, "done")
	<-ch // drain the text chunk

	state.Fail(context.Background(), ch, errors.New("boom"))
	errChunk := <-ch
	if errChunk.Type != llm.ChunkTypeError || errChunk.Error == nil || errChunk.Error.Error() != "boom" {
		t.Fatalf("unexpected error chunk: %#v", errChunk)
	}

	state.Finish(context.Background(), ch)
	doneChunk := <-ch
	if doneChunk.Type != llm.ChunkTypeDone || doneChunk.Response == nil {
		t.Fatalf("unexpected done chunk: %#v", doneChunk)
	}
	if doneChunk.Response.Content != "done" {
		t.Fatalf("expected final response content to be preserved, got %#v", doneChunk.Response)
	}
}
