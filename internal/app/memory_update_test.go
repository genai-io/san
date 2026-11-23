package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

const memoryUpdateProposal = `{"action":"remember","file":"fixture-build.md","name":"Build convention","description":"Project build command","type":"project","body":"Use make for project builds."}`

func memoryUpdateFixture(t *testing.T) (*model, *aitest.Driver, []core.Message, string) {
	t.Helper()
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	messages := []core.Message{core.UserMessage("This project uses make for builds.", nil), core.AssistantMessage("Understood: make builds the project.", "", nil)}
	messages[0].ID, messages[1].ID = core.NewMessageID(), core.NewMessageID()
	m, _ := resumedModel(t, messages[:1])
	driver := aitest.New(aitest.Says(memoryUpdateProposal))
	m.env.LLMProvider = &finalizationProvider{client: driver.Client()}
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "test"}
	owner := m.services.Session
	h, err := owner.StartInference(t.Context(), session.InferenceOptions{
		Purpose: session.PurposeTurn, TurnID: "test-turn", Provider: m.env.LLMProvider.Name(), Model: ai.Model{ID: "test"},
		Request: &ai.Request{System: "main rules", Messages: messages[:1], Tools: []ai.Schema{{Name: "Read", Definition: map[string]any{"type": "object"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Finish(t.Context(), &ai.Response{StopReason: ai.StopEndTurn, Content: messages[1].Content}, nil,
		session.Event{Payload: session.MessageAppendPayload{Messages: messages[1:]}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.cancelMemoryUpdate)
	return m, driver, messages, filepath.Join(string(m.autoMemoryStore()), "fixture-build.md")
}

func TestManualMemoryUpdateWritesThroughAside(t *testing.T) {
	m, driver, _, path := memoryUpdateFixture(t)
	if text, cmd, _ := m.executeCommand(t.Context(), "/memory maintain"); cmd != nil || m.memoryUpdate != nil || !strings.Contains(text, "update") {
		t.Fatalf("removed command must not start an update: %q", text)
	}
	text, propose, handled := m.executeCommand(t.Context(), "/memory update")
	if !handled || text != "" || propose == nil {
		t.Fatalf("command was not dispatched: %q, %v", text, handled)
	}
	proposed := propose().(memoryUpdateProposed)
	if proposed.err != nil || proposed.change == nil {
		t.Fatalf("proposal failed: %v", proposed.err)
	}
	request := driver.Last()
	if request.System != "main rules" || request.ToolChoice != ai.ToolChoiceNone || len(request.Tools) != 1 || len(request.Messages) != 3 {
		t.Fatalf("Memory Update did not retain the main prefix: %+v", request)
	}
	if request.Messages[0].Text() != "This project uses make for builds." || request.Messages[1].Text() != "Understood: make builds the project." ||
		!strings.Contains(request.Messages[2].Text(), `"enabled":["remember","update","forget"]`) {
		t.Fatalf("main dialogue was changed or Memory Update instructions were not appended: %+v", request.Messages)
	}
	write := m.applyMemoryUpdate(proposed)
	if write == nil {
		t.Fatal("accepted proposal did not return an apply command")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("proposal wrote before its apply command: %v", err)
	}
	done := write().(memoryUpdateFinished)
	if done.err != nil || !strings.Contains(done.notice, "saved fixture-build.md") {
		t.Fatalf("Aside write failed: %+v", done)
	}
	m.finishMemoryUpdate(done)
	if contents, err := os.ReadFile(path); err != nil || !strings.Contains(string(contents), "Use make for project builds.") {
		t.Fatalf("note not saved: %q, %v", contents, err)
	}
	snapshot := m.services.Session.Snapshot()
	live := m.conv.Messages[len(m.conv.Messages)-1]
	rows := m.services.SessionStore.ChatRows(snapshot)
	replayed := rows[len(rows)-1]
	if len(snapshot.Messages()) != 2 || !strings.Contains(live.Content, "saved fixture-build.md") ||
		!live.AutoMemoryUpdated || !replayed.AutoMemoryUpdated || replayed.Content != live.Content {
		t.Fatal("memory update should add only a display notice, not a main message")
	}
	var recorded bool
	for _, event := range snapshot.Events() {
		if start, ok := event.Payload.(*session.InferenceStartPayload); ok && start.Purpose == session.PurposeMemoryUpdate {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("missing Memory Update Aside recording")
	}
}

func TestAutomaticMemoryUpdateRespectsLivePolicy(t *testing.T) {
	m, driver, messages, path := memoryUpdateFixture(t)
	result := core.Result{StopReason: core.StopEndTurn, Steps: 1, Messages: messages}
	if cmd := m.updateMemoryAfterTurn(result); cmd != nil {
		t.Fatal("automatic writes should be off by default")
	}
	data := setting.NewData()
	data.MemoryUpdate = new(true)
	m.services.Setting = setting.New(data)
	cmd := m.updateMemoryAfterTurn(result)
	if cmd == nil {
		t.Fatal("enabled completed turn did not start a memory update")
	}
	proposed := cmd().(memoryUpdateProposed)
	if proposed.err != nil || proposed.change == nil || !strings.Contains(driver.Last().Messages[2].Text(), `"enabled":["remember","update","forget"]`) {
		t.Fatalf("Update did not review all three operations: %v", proposed.err)
	}
	m.services.Setting = setting.New(setting.NewData())
	if cmd := m.applyMemoryUpdate(proposed); cmd != nil {
		t.Fatal("disabled setting did not discard in-flight proposal")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("discarded proposal wrote memory: %v", err)
	}
}

func TestMemoryUpdateSwitchCancelsScheduledWrite(t *testing.T) {
	m, _, _, path := memoryUpdateFixture(t)
	_, propose := m.updateMemory()
	if propose == nil {
		t.Fatal("missing proposal command")
	}
	write := m.applyMemoryUpdate(propose().(memoryUpdateProposed))
	if write == nil {
		t.Fatal("missing write")
	}
	m.cancelMemoryUpdate()
	result := write().(memoryUpdateFinished)
	if _, err := os.Stat(path); !errors.Is(result.err, context.Canceled) || !os.IsNotExist(err) {
		t.Fatalf("cancelled Aside still wrote: result=%v, stat=%v", result.err, err)
	}
}
