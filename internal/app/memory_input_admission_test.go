package app

import (
	"context"
	"errors"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestFlowMemoryMaintenanceAdmission(t *testing.T) {
	for _, mode := range []string{"removed", "replaced", "edited", "editing", "retired", "accepted"} {
		t.Run(mode, func(t *testing.T) {
			m, note := memoryAdmissionModel(t)
			m.flowMain = newFlowMainBridge()
			owner := m.services.Session
			executor, err := m.flowMain.bind(owner).Open(t.Context(), flow.MainRunInfo{SessionID: owner.ID()})
			if err != nil {
				t.Fatal(err)
			}
			lease := executor.(*flowMainLease)
			m.flowMainRequest = &flowMainRequest{msg: flowMainMsg{lease: lease, ctx: t.Context()}, messageID: "writer", outbox: m.AgentOutbox()}
			const text = "remember user memory: Use make."
			m.userInput.Queue.Enqueue(text, nil)
			started, release := blockMemoryProposal(m)
			cmd, ok := m.releaseFlowMainStepInput()
			if !ok || cmd == nil {
				t.Fatal("memory input was not prepared")
			}
			result := runMemoryPreparation(cmd)
			waitMemoryTest(t, started)
			switch mode {
			case "removed":
				m.userInput.Queue.Dequeue()
			case "replaced":
				m.userInput.Queue.Dequeue()
				m.userInput.Queue.Enqueue(text, nil) // Same text, different queue identity.
			case "edited":
				m.userInput.Queue.UpdateAt(0, "remember user memory: Use ninja.", nil)
			case "editing":
				m.userInput.Queue.SelectIdx = 0
			case "retired":
				m.flowMain.bind(nil)
			}
			close(release)
			reply := waitMemoryTest(t, result)
			assertNoMemoryWrite(t, note) // Even a valid proposal must wait for Update.
			m.onMemoryInput(reply)
			if mode == "accepted" {
				assertMemorySuccess(t, m, note)
			} else {
				assertNoMemoryWrite(t, note)
				assertMemoryRecording(t, m, 0)
			}
		})
	}
}

func TestMemoryRecallCarriesWaitIntoSubmission(t *testing.T) {
	m, _ := memoryAdmissionModel(t)
	head := pendingMessage{Message: core.UserMessage("How do we build?", nil), FromUser: true}
	m.pendingMessages.Enqueue(head)
	m.prepareMemoryInput(head)
	request := m.memoryInput
	m.onMemoryInput(memoryInputMsg{input: request, result: agent.PreparedMemory{Message: head.Message}})
	if m.memoryInput != nil || !m.conv.Stream.Waiting || m.conv.Stream.StartedAt != request.startedAt {
		t.Fatal("recall did not hand the wait clock to the main turn")
	}
}

func TestTUIMemoryMaintenanceStoreChanged(t *testing.T) {
	m, oldNote := memoryAdmissionModel(t)
	m.pendingMessages.Enqueue(pendingMessage{Message: core.UserMessage("remember user memory: Use make.", nil), FromUser: true})
	started, release := blockMemoryProposal(m)
	result := runMemoryPreparation(m.prepareMemoryInput(m.pendingMessages.First()))
	waitMemoryTest(t, started)
	newDir := t.TempDir()
	if err := setting.SaveMemoryDirectory(setting.ScopeUser, m.env.CWD, newDir); err != nil {
		t.Fatal(err)
	}
	close(release)
	reply := waitMemoryTest(t, result)
	m.env.LLMProvider = &finalizationProvider{client: aitest.New(aitest.Says(memoryTestProposal)).Client()}
	cmd := m.onMemoryInput(reply)
	assertNoMemoryWrite(t, oldNote)
	assertMemoryRecording(t, m, 0)
	if cmd == nil || m.pendingMessages.Len() != 1 {
		t.Fatal("obsolete store result consumed the pending request")
	}
	m.onMemoryInput(waitMemoryTest(t, runMemoryPreparation(cmd)))
	assertNoMemoryWrite(t, oldNote)
	assertMemorySuccess(t, m, filepath.Join(newDir, "build.md"))
}

func TestTUIMemoryMaintenanceOrdinaryNotices(t *testing.T) {
	for _, tc := range []struct{ proposal, notice string }{
		{`{"action":"none"}`, "No memory changes."},
		{`{"action":"forget","file":"build.md"}`, "Auto Memory could not finish: invalid memory proposal"},
	} {
		t.Run(tc.notice, func(t *testing.T) {
			m, note := memoryAdmissionModel(t)
			m.env.LLMProvider = &finalizationProvider{client: aitest.New(aitest.Says(tc.proposal)).Client()}
			m.pendingMessages.Enqueue(pendingMessage{Message: core.UserMessage("remember user memory: Use make.", nil), FromUser: true})
			m.onMemoryInput(waitMemoryTest(t, runMemoryPreparation(m.prepareMemoryInput(m.pendingMessages.First()))))
			assertNoMemoryWrite(t, note)
			assertMemoryRecording(t, m, 1)
			if last := m.conv.Messages[len(m.conv.Messages)-1]; last.Role != core.ChatNotice || last.Content != tc.notice {
				t.Fatalf("last message = %+v; want %q", last, tc.notice)
			}
		})
	}
}

const memoryTestProposal = `{"action":"remember","file":"build.md","name":"Build","description":"Build preference","type":"user","body":"Use make."}`

func memoryAdmissionModel(t *testing.T) (*model, string) {
	t.Helper()
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	m, _ := compactQueueModel(t)
	m.conv.OnNotice = m.recordNotice
	return m, filepath.Join(m.autoMemory().User, "build.md")
}

func blockMemoryProposal(m *model) (<-chan struct{}, chan struct{}) {
	started, release := make(chan struct{}), make(chan struct{})
	m.env.LLMProvider = &finalizationProvider{client: aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return aitest.Says(memoryTestProposal)(ctx, req)
	}).Client()}
	return started, release
}

func runMemoryPreparation(cmd tea.Cmd) <-chan memoryInputMsg {
	result := make(chan memoryInputMsg, 1)
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				go run(child)
			}
		case memoryInputMsg:
			result <- msg
		}
	}
	go run(cmd)
	return result
}

func waitMemoryTest[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("memory proposal did not return")
		var zero T
		return zero
	}
}

func assertNoMemoryWrite(t *testing.T, note string) {
	t.Helper()
	if _, err := os.Stat(note); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected memory file %s: %v", note, err)
	}
}

func assertMemorySuccess(t *testing.T, m *model, note string) {
	t.Helper()
	if _, err := os.Stat(note); err != nil {
		t.Fatal(err)
	}
	live := 0
	for _, row := range m.conv.Messages {
		if row.Role == core.ChatNotice && row.Content == "Memory · saved build.md to user" {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("got %d live success notices; want 1", live)
	}
	assertMemoryRecording(t, m, 1)
}

func assertMemoryRecording(t *testing.T, m *model, wantNotices int) {
	t.Helper()
	snapshot := m.services.Session.Snapshot()
	if len(snapshot.Unfinished().Inferences) != 0 {
		t.Fatal("memory proposal recording was not closed")
	}
	notices, finishes := 0, 0
	for _, ev := range snapshot.Events() {
		switch ev.Type {
		case session.EventAnnotationAdd:
			notices++
		case session.EventInferenceFinish:
			finishes++
		}
	}
	if finishes == 0 {
		t.Fatal("raw memory proposal was not recorded")
	}
	if notices != wantNotices {
		t.Fatalf("got %d recorded notices; want %d", notices, wantNotices)
	}
}
