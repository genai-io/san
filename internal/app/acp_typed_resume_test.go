package app

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPLoadTypedCompactionAndUnfinished(t *testing.T) {
	driver := aitest.New(aitest.Says("continued"))
	f := newACPFixture(t, driver, nil)
	cwd := t.TempDir()
	store, err := session.OpenStore(f.ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(f.ctx, session.CreateOptions{InitialCWD: cwd, Config: &session.EventConfig{Model: new("test")}})
	if err != nil {
		t.Fatal(err)
	}
	old := core.UserMessage("full display history before compaction", nil)
	old.ID = "old"
	if err := owner.Append(f.ctx, session.Event{Payload: session.MessageAppendPayload{Messages: []core.Message{old}}}); err != nil {
		t.Fatal(err)
	}
	summary := core.UserMessage("compact active summary", nil)
	summary.ID = "summary"
	if err := owner.Append(f.ctx, session.Event{Payload: session.ContextCompactPayload{Through: 2, Summary: summary}}); err != nil {
		t.Fatal(err)
	}
	inference, err := owner.StartInference(f.ctx, session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "interrupted", Provider: "test", Model: ai.Model{ID: "test"}, Request: &ai.Request{Messages: []ai.Message{summary}}})
	if err != nil {
		t.Fatal(err)
	}
	call := core.AssistantMessage("", "", []core.ToolCall{{ID: "interrupted-tool", Name: "Bash", Input: `{"command":"true"}`}})
	call.ID = "interrupted-message"
	ref := inference.Ref()
	if err := owner.Append(f.ctx, session.Event{Source: &ref, Payload: session.MessageAppendPayload{Messages: []core.Message{call}}}); err != nil {
		t.Fatal(err)
	}
	id := protocol.SessionID(owner.ID())
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	var response protocol.LoadSessionResponse
	if err := f.client.Call(f.ctx, "session/load", protocol.LoadSessionRequest{SessionID: id, CWD: cwd, MCPServers: []protocol.MCPServer{}}, &response); err != nil {
		t.Fatal(err)
	}
	s := f.host.sessions[id]
	active := s.runtime.Messages()
	if len(active) != 2 || !strings.Contains(active[0].Text(), "compact active summary") {
		t.Fatalf("active context = %+v", active)
	}
	if driver.Calls() != 0 {
		t.Fatal("resume replayed model work")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var display, interrupted bool
	for _, notification := range f.updates {
		switch update := notification.Update.Value.(type) {
		case protocol.SessionUpdateUserMessageChunkVariant:
			if text, ok := update.Content.Value.(protocol.ContentBlockTextVariant); ok && strings.Contains(text.Text, "full display history") {
				display = true
			}
		case protocol.SessionUpdateAgentMessageChunkVariant:
			if text, ok := update.Content.Value.(protocol.ContentBlockTextVariant); ok && strings.Contains(text.Text, "tools that never ran [interrupted-tool]") {
				interrupted = true
			}
		case protocol.SessionUpdateToolCallVariant:
			if update.ToolCallID == "interrupted-tool" && update.Status.Value == protocol.ToolCallStatusCompleted {
				t.Fatal("unfinished tool rendered completed")
			}
		}
	}
	if !display || !interrupted {
		t.Fatalf("full history=%v interrupted notice=%v", display, interrupted)
	}
}

func TestACPTypedFinalPermissionRetainsQuestionAndAuthority(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Asks(core.ToolCall{ID: "denied-write", Name: "Write", Input: `{"file_path":"denied.txt","content":"no"}`}), aitest.Says("denied")), func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("reject_once"), nil
	})
	id := f.session(t, t.TempDir())
	if _, err := f.prompt(id, "try writing"); err != nil {
		t.Fatal(err)
	}
	snapshot := f.host.sessions[id].nativeSession.Session.Snapshot()
	var count int
	for _, event := range snapshot.Events() {
		if decision, ok := event.Payload.(*session.PermissionDecidePayload); ok && decision.CallID == "denied-write" {
			count++
			if decision.Decision != "deny" || decision.By != "user" || decision.Scope != "once" || decision.Asked == nil || len(decision.Asked.Options) != 2 || decision.Asked.At == 0 || !strings.Contains(string(decision.Asked.Detail), "denied.txt") {
				t.Fatalf("final permission lost authority or question: %+v", decision)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one final native decision, got %d", count)
	}
}

func TestACPLoadChecksTypedExecutionCWDBeforeExtensions(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("unused")), nil)
	cwd := t.TempDir()
	store, err := session.OpenStore(f.ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(f.ctx, session.CreateOptions{InitialCWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Append(f.ctx, session.Event{Payload: session.SessionUpdatePayload{State: &session.StatePatch{ExecutionCWD: session.SetField(t.TempDir())}}}); err != nil {
		t.Fatal(err)
	}
	id := protocol.SessionID(owner.ID())
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = f.host.open(f.ctx, protocol.NewSessionRequest{CWD: cwd, MCPServers: []protocol.MCPServer{}}, string(id))
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected execution directory rejection, got %v", err)
	}
	f.host.mu.Lock()
	defer f.host.mu.Unlock()
	if f.host.sessions[id] != nil {
		t.Fatal("rejected session installed")
	}
}
