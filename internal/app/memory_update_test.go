package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

const memoryUpdateProposal = `{"action":"remember","file":"fixture-build.md","name":"Build convention","description":"Project build command","type":"project","body":"Use make for project builds."}`

func memoryUpdateFixture(t *testing.T, turns ...aitest.Turn) (*model, *aitest.Driver, []core.Message, string) {
	t.Helper()
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	messages := []core.Message{core.UserMessage("This project uses make for builds.", nil), core.AssistantMessage("Understood: make builds the project.", "", nil)}
	messages[0].ID, messages[1].ID = core.NewMessageID(), core.NewMessageID()
	m, _ := resumedModel(t, messages[:1])
	if len(turns) == 0 {
		turns = []aitest.Turn{aitest.Says(memoryUpdateProposal)}
	}
	driver := aitest.New(turns...)
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

func TestMemoryUpdateWritesThroughAside(t *testing.T) {
	t.Run("manual", func(t *testing.T) { testMemoryUpdateWrite(t, false) })
	t.Run("automatic", func(t *testing.T) { testMemoryUpdateWrite(t, true) })
}

func testMemoryUpdateWrite(t *testing.T, automatic bool) {
	t.Helper()
	const read = `{"action":"read","files":["fixture-build.md"]}`
	const update = `{"action":"update","file":"fixture-build.md","old":"Build with go build.","replacement":"Use make for project builds."}`
	m, driver, messages, path := memoryUpdateFixture(t, aitest.Says(read), aitest.Says(update))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(filepath.Dir(path), system.AutoMemoryIndexName)
	const indexText = "- [Build](fixture-build.md) — Project build command\n"
	if err := os.WriteFile(index, []byte(indexText), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Build with go build.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var proposed memoryUpdateProposed
	if automatic {
		data := setting.NewData()
		data.MemoryUpdate = new(true)
		m.services.Setting = setting.New(data)
		propose := m.updateMemoryAfterTurn(core.Result{StopReason: core.StopEndTurn, Steps: 1, Messages: messages})
		if propose == nil || m.memoryUpdate == nil || m.memoryUpdate.manual {
			t.Fatal("completed turn did not schedule an automatic update")
		}
		proposed = propose().(memoryUpdateProposed)
	} else {
		if text, cmd, _ := m.executeCommand(t.Context(), "/memory update"); cmd != nil || m.memoryUpdate != nil || !strings.HasPrefix(text, "Usage: /memory") || strings.Contains(text, "update") {
			t.Fatalf("removed command must not start or advertise an update: %q", text)
		}
		text, propose, handled := m.executeCommand(t.Context(), "/reflect memory")
		if !handled || text != "" || propose == nil || m.memoryUpdate == nil || !m.memoryUpdate.manual {
			t.Fatalf("command did not schedule a manual update: %q, %v", text, handled)
		}
		proposed = propose().(memoryUpdateProposed)
	}
	if proposed.err != nil || proposed.change == nil {
		t.Fatalf("proposal failed: %v", proposed.err)
	}
	calls := driver.Sent()
	if len(calls) != 2 {
		t.Fatalf("want an index request and one continuation, got %d calls", len(calls))
	}
	request := calls[0]
	if request.System != "main rules" || request.ToolChoice != ai.ToolChoiceAuto || len(request.Tools) != 1 || len(request.Messages) != 3 {
		t.Fatalf("Memory Update did not retain the main prefix: %+v", request)
	}
	blocks := request.Messages[2].Content
	if request.Messages[0].Text() != "This project uses make for builds." || request.Messages[1].Text() != "Understood: make builds the project." ||
		len(blocks) != 2 || blocks[1].Text != indexText || !strings.Contains(blocks[0].Text, "## Note types for new notes") ||
		strings.Contains(blocks[0].Text, "Build with go build.") {
		t.Fatalf("wrong initial Memory Update context: %+v", request.Messages)
	}
	followup := calls[1]
	if len(followup.Messages) != 5 || followup.Messages[3].Text() != read ||
		!strings.Contains(followup.Messages[4].Text(), "Build with go build.") {
		t.Fatalf("requested note missing from follow-up: %+v", followup.Messages)
	}
	write := m.applyMemoryUpdate(proposed)
	if write == nil {
		t.Fatal("accepted proposal did not return an apply command")
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "Build with go build.\n" {
		t.Fatalf("proposal wrote before its apply command: %q, %v", contents, err)
	}
	done := write().(memoryUpdateFinished)
	if done.err != nil || !strings.Contains(done.notice, "updated fixture-build.md") {
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
	if len(snapshot.Messages()) != 2 || !strings.Contains(live.Content, "updated fixture-build.md") ||
		!live.AutoMemoryUpdated || !replayed.AutoMemoryUpdated || replayed.Content != live.Content {
		t.Fatal("memory update should add only a display notice, not a main message")
	}
	var starts []uint64
	var outputs []string
	var noticeSource uint64
	for _, event := range snapshot.Events() {
		switch p := event.Payload.(type) {
		case *session.InferenceStartPayload:
			if p.Purpose == session.PurposeMemoryUpdate {
				starts = append(starts, event.Seq)
			}
		case *session.InferenceFinishPayload:
			if event.Source != nil && len(starts) > 0 && event.Source.Seq == starts[len(starts)-1] && p.Output != nil {
				outputs = append(outputs, p.Output.Text)
			}
		case *session.AnnotationAddPayload:
			if p.Text == done.notice && event.Source != nil {
				noticeSource = event.Source.Seq
			}
		}
	}
	if len(starts) != 2 || len(outputs) != 2 || outputs[0] != read || outputs[1] != update || noticeSource != starts[1] {
		t.Fatalf("read and proposal should be recorded separately, with only the final call linked to the write: %v, %v, %d", starts, outputs, noticeSource)
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
	if proposed.err != nil || proposed.change == nil || !strings.Contains(driver.Last().Messages[2].Content[0].Text, "## Note types for new notes") {
		t.Fatalf("Update did not offer additions: %v", proposed.err)
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
