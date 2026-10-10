package app

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
)

func TestIdleCtrlOInspectsCommittedResultsAndPreservesDraft(t *testing.T) {
	m := commitTestModel(turnOf("done", "tc")...)
	m.env.Ready = true
	m.conv.CommittedCount = len(m.conv.Messages)
	m.userInput.Textarea.SetValue("next question\n还没写完")
	m.userInput.Textarea.CursorStart()
	beforeCursor := m.userInput.Textarea.Cursor()
	before := m.conv.CommittedCount
	cmd, handled := m.routeKeypress(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !handled || cmd == nil || !m.userInput.Transcript.IsActive() {
		t.Fatal("idle Ctrl+O should immediately open a loading viewer")
	}
	m.dispatch(cmd())
	if !m.View().AltScreen || !strings.Contains(ansi.Strip(m.userInput.Transcript.Render()), "out done") {
		t.Fatal("completed result should open expanded in the alternate screen")
	}
	m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.userInput.Transcript.IsActive() || m.conv.CommittedCount != before || len(m.flush.pendingPrints) != 0 {
		t.Fatal("viewing history changed the native commit state")
	}
	if m.userInput.Textarea.Value() != "next question\n还没写完" || !reflect.DeepEqual(beforeCursor, m.userInput.Textarea.Cursor()) {
		t.Fatal("opening and closing history changed the draft or cursor")
	}
	if m.conv.Messages[2].Expanded {
		t.Fatal("viewer expansion leaked into the source messages")
	}
}

func TestRunningCtrlOKeepsInlineExpansion(t *testing.T) {
	m := commitTestModel(turnOf("live", "tc")...)
	m.conv.Stream.Active = true
	if cmd := m.handleCtrlO(); cmd == nil || m.userInput.Transcript.IsActive() {
		t.Fatal("running Ctrl+O should retain its single/double tap behavior")
	}
	m.handleCtrlOSingleTick()
	if !m.conv.Messages[2].Expanded || m.userInput.Transcript.IsActive() {
		t.Fatal("running Ctrl+O should expand the live result inline")
	}
	// A turn may finish during the double-tap window.
	m.userInput.LastCtrlO = time.Now()
	m.conv.Stream.Active = false
	if cmd := m.handleCtrlOSingleTick(); cmd == nil || !m.userInput.Transcript.IsActive() {
		t.Fatal("a tick after turn completion should open history")
	}
}

func TestHistoryCommandOpensTheSameViewer(t *testing.T) {
	m := commitTestModel(turnOf("command", "tc")...)
	text, cmd, handled := m.executeCommand(context.Background(), "/history")
	if !handled || text != "" || cmd == nil {
		t.Fatal("/history should open the viewer through the app callback")
	}
	m.dispatch(cmd())
	if !strings.Contains(ansi.Strip(m.userInput.Transcript.Render()), "out command") {
		t.Fatal("/history should expand the latest result just like idle Ctrl+O")
	}
}

func TestHistoryShowsCancelledOrphanResult(t *testing.T) {
	m := commitTestModel(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{
		ToolCallID: "cancelled", ToolName: "Read", IsError: true, Content: ai.TextContent("Error: cancelled by user"),
	}})
	entries := m.historyEntries(m.conv.Messages)
	if len(entries) != 1 || !entries[0].Tool || !strings.Contains(ansi.Strip(entries[0].Render(80, true)), "cancelled by user") {
		t.Fatal("a cancelled result should remain readable even without its call")
	}
}

func TestHistoryParallelResultsExpandIndependently(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.ChatAssistant, Content: "already printed", ContentCommittedLen: len("already printed"), BulletEmitted: true,
			ToolCalls: []core.ToolCall{{ID: "a", Name: "Read", Input: `{"file_path":"a.go"}`}, {ID: "b", Name: "Read", Input: `{"file_path":"b.go"}`}}},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "b", ToolName: "Read", Content: ai.TextContent("B_ONLY\n" + strings.Repeat("b\n", 40))}},
		{Role: core.ChatNotice, Content: "background notice"},
		{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "a", ToolName: "Read", Content: ai.TextContent("A_ONLY\n" + strings.Repeat("a\n", 40))}},
	}
	m := commitTestModel(messages...)
	entries := m.historyEntries(messages)
	if len(entries) != 4 || !entries[1].Tool || !entries[2].Tool {
		t.Fatalf("expected one message, two tools and a notice, got %d entries", len(entries))
	}
	if text := ansi.Strip(entries[0].Render(80, false)); !strings.Contains(text, "already printed") {
		t.Fatalf("history must ignore streaming commit offsets: %q", text)
	}
	for i, want := range []string{"A_ONLY", "B_ONLY"} {
		entry := entries[i+1]
		if strings.Contains(ansi.Strip(entry.Render(80, false)), want) {
			t.Fatal("collapsed result should be a summary")
		}
		text := ansi.Strip(entry.Render(80, true))
		if !strings.Contains(text, want) || strings.Contains(text, []string{"B_ONLY", "A_ONLY"}[i]) {
			t.Fatalf("result paired with the wrong call: %q", text)
		}
	}
	if messages[0].ContentCommittedLen == 0 || messages[1].Expanded || messages[3].Expanded {
		t.Fatal("history mutated source display state")
	}
}

func TestHistoryLoadIgnoresClosedOrReplacedViewer(t *testing.T) {
	m := commitTestModel(turnOf("old", "tc")...)
	first := m.openHistory()
	m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.historyLoaded(first().(historyLoadedMsg))
	if m.userInput.Transcript.IsActive() {
		t.Fatal("a completed load reopened the closed viewer")
	}
	first = m.openHistory()
	m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.conv.Messages = nil
	second := m.openHistory()
	m.historyLoaded(first().(historyLoadedMsg))
	if !strings.Contains(m.userInput.Transcript.Render(), "Loading") {
		t.Fatal("stale load overwrote a newer viewer")
	}
	m.historyLoaded(second().(historyLoadedMsg))
	if !strings.Contains(m.userInput.Transcript.Render(), "No messages yet") {
		t.Fatal("empty history should have an explicit empty state")
	}
}

func TestHistoryLoadReadsStoreAndSurfacesCorruption(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := &session.Snapshot{Messages: turnOf("stored", "tc")}
	if err := store.Save(snap); err != nil {
		t.Fatal(err)
	}
	m := commitTestModel(core.ChatMessage{Role: core.ChatUser, Content: "only the current context"})
	m.services.Session = &session.Setup{Store: store, SessionID: snap.Metadata.ID}
	load := m.openHistory()
	m.historyLoaded(load().(historyLoadedMsg))
	if !strings.Contains(ansi.Strip(m.userInput.Transcript.Render()), "out stored") {
		t.Fatal("history did not read persisted messages")
	}
	if err := os.WriteFile(store.SessionPath(snap.Metadata.ID), []byte("broken record\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	load = m.openHistory()
	m.historyLoaded(load().(historyLoadedMsg))
	if !strings.Contains(m.userInput.Transcript.Render(), "Unable to read session history") {
		t.Fatal("corrupt history must be reported, not silently replaced with current context")
	}
	// The unsaved first turn still has its in-memory messages.
	m.services.Session.SetID("not-saved-yet")
	load = m.openHistory()
	m.historyLoaded(load().(historyLoadedMsg))
	if !strings.Contains(ansi.Strip(m.userInput.Transcript.Render()), "only the current context") {
		t.Fatal("missing transcript should use the current conversation")
	}
	msg := historyLoadedMsg{generation: m.userInput.Transcript.Generation(), sessionID: "not-saved-yet", err: errors.New("permission denied")}
	m.historyLoaded(msg)
	if !strings.Contains(m.userInput.Transcript.Render(), "permission denied") {
		t.Fatal("read errors should remain visible")
	}
}

func TestHistoryToolOutputRewrapsWithoutLosingContent(t *testing.T) {
	messages := turnOf("long", "tc")
	messages[2].ToolResult.Content = ai.TextContent(strings.Repeat("中文abcdefgh", 30) + "TAIL_SENTINEL")
	m := commitTestModel(messages...)
	entries := m.historyEntries(messages)
	for _, width := range []int{80, 30, 120} {
		text := ansi.Strip(entries[len(entries)-1].Render(width, true))
		body := strings.ReplaceAll(strings.ReplaceAll(text, "  ┊ ", ""), "\n", "")
		if !strings.Contains(body, "TAIL_SENTINEL") {
			t.Fatalf("width %d lost the end of the result", width)
		}
		for _, line := range strings.Split(text, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d produced an overflowing row: %q", width, line)
			}
		}
	}
}
