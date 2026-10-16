package conv

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type streamRuntime struct{ Runtime }

func (*streamRuntime) CommitMessages() []tea.Cmd       { return nil }
func (*streamRuntime) FlushStreamingBlocks() []tea.Cmd { return nil }
func (*streamRuntime) OnInference(*ai.Response)        {}

func TestInferenceIdentitySurvivesNoticesAndDefensiveDone(t *testing.T) {
	// sdk-go consumes EventDone internally. This covers defensive handling;
	// app/model_inference_lifecycle_test.go exercises the normal terminal path.
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{})
	owner := m.Stream.MessageID
	m.AddAgentNotice("background completed")
	applyChunk(rt, &m, ai.Event{Type: ai.EventBlockDelta, Block: ai.Block{Type: ai.BlockText, Text: "reply"}})
	response := &ai.Response{Content: ai.Content{{Type: ai.BlockThinking, Signature: "signature"}}}
	applyChunk(rt, &m, ai.Event{Type: ai.EventDone, Response: response})
	if m.Stream.Active || m.Stream.MessageID != owner {
		t.Fatal("Done lost the identity before final metadata arrived")
	}
	applyAgentEvent(rt, &m, sdkagent.InferenceEnd{Response: response})
	if m.Stream.MessageID != "" || m.Messages[0].ID != owner || m.Messages[0].Content != "reply" || m.Messages[0].ThinkingSignature != "signature" {
		t.Fatalf("metadata did not reach its inference: %+v", m)
	}
	if m.Messages[1].Content != "background completed" {
		t.Fatal("inference corrupted the notice")
	}
}

func TestFailedInferencePreservesIdentityForRetry(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{})
	owner := m.Stream.MessageID
	applyAgentEvent(rt, &m, sdkagent.InferenceEnd{Attempt: 1, Err: errors.New("provider unavailable"), Response: &ai.Response{Content: ai.Content{{
		Type: ai.BlockToolCall, ToolCall: &ai.ToolCall{ID: "discarded", Name: "Read"},
	}}}})
	if m.Stream.MessageID != owner || len(m.Tool.PendingCalls) != 0 || len(m.Messages) != 2 || m.Messages[1].Content != "Inference attempt 1 failed: provider unavailable" {
		t.Fatalf("failed attempt accepted tools or hid its error: %+v", m)
	}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 2})
	if len(m.Messages) != 2 || m.Messages[0].Content != "Inference attempt 1 failed: provider unavailable" || m.Stream.MessageID == owner {
		t.Fatalf("retry did not replace its failed inference: %+v", m)
	}
}

func TestCanceledInferenceCannotReviveTools(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{})
	m.Stream.Stop()
	m.AddNotice("cancelled")
	applyAgentEvent(rt, &m, sdkagent.InferenceEnd{Response: &ai.Response{Content: ai.Content{{
		Type: ai.BlockToolCall, ToolCall: &ai.ToolCall{ID: "late", Name: "Read"},
	}}}})
	if len(m.Tool.PendingCalls) != 0 || len(m.Messages[0].ToolCalls) != 0 {
		t.Fatal("late InferenceEnd revived a cancelled tool call")
	}
}

func TestInferenceRetryPreservesNoticeAndRebindsIdentity(t *testing.T) {
	m, rt := NewModel(80), &streamRuntime{}
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{})
	old := m.Stream.MessageID
	m.AppendStream("partial reply", "")
	m.AddNotice("background error")
	applyAgentEvent(rt, &m, sdkagent.InferenceStart{Attempt: 2})
	m.AppendStream("retried reply", "")
	if m.Stream.MessageID == "" || m.Stream.MessageID == old || len(m.Messages) != 2 || m.Messages[0].Content != "background error" || m.Messages[1].Content != "retried reply" {
		t.Fatalf("retry lost the notice or streamed into the old message: %+v", m)
	}
}
