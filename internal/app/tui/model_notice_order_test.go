package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestCompletionNoticeAboveLiveAgentAcrossCommit(t *testing.T) {
	m := fixedComposerModel(100, 40)
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"description":"live-worker","prompt":"work"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "prior-answer"})
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="notice-token">done</task-notification>`})
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}})
	m.conv.Tool.PendingCalls = []core.ToolCall{call}
	check := func(phase string) {
		t.Helper()
		out := ansi.Strip(m.renderChatSection(chat.RenderActiveBody(m.messageRenderParams())))
		last := -1
		for _, token := range []string{"prior-answer", "notice-token", "live-worker"} {
			at := strings.Index(out, token)
			if at <= last || strings.Count(out, token) != 1 {
				t.Fatalf("%s: order/count of %s: %s", phase, token, out)
			}
			last = at
		}
	}
	check("first frame")
	short := ansi.Strip(m.chatTailAbove(strings.Repeat("\n", 38), m.messageRenderParams()))
	if notice, worker := strings.Index(short, "notice-token"), strings.Index(short, "live-worker"); notice < 0 || worker <= notice || strings.Count(short, "notice-token") != 1 {
		t.Fatalf("first compact frame: %s", short)
	}
	m.CommitMessages()
	if m.conv.CommittedCount != 2 {
		t.Fatalf("committed %d messages", m.conv.CommittedCount)
	}
	check("after commit")
	m.CommitMessages()
	check("repeat commit")
}

func TestCompletionNoticeStaysAboveLaterEditDiff(t *testing.T) {
	m := fixedComposerModel(100, 40)
	edit := core.ToolCall{ID: "edit", Name: tool.ToolEdit, Input: `{"file_path":"example.go"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "before-notice", ToolCalls: []core.ToolCall{edit}})
	m.conv.Tool.Track([]core.ToolCall{edit})
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="review-worker">done</task-notification>`})
	frame := func() string {
		return ansi.Strip(m.renderChatSection(chat.RenderActiveBody(m.messageRenderParams())))
	}
	check := func(phase, out string, tokens ...string) {
		t.Helper()
		last := -1
		for _, token := range tokens {
			at := strings.Index(out, token)
			if at <= last || strings.Count(out, token) != 1 {
				t.Fatalf("%s: order/count of %q: %s", phase, token, out)
			}
			last = at
		}
	}
	check("running", frame(), "before-notice", "review-worker · Completed", "Edit(example.go)")
	m.conv.Tool.MarkComplete(edit.ID)
	m.conv.Append(core.ChatMessage{
		Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: edit.ID, ToolName: edit.Name, Content: ai.TextContent("edited")},
		ToolDetails: toolresult.FileChangeDetails{UnifiedDiff: "@@ -1 +1 @@\n-old-value\n+new-value\n", AddedLines: 1, RemovedLines: 1},
	})
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="another-worker">done</task-notification>`})
	check("settled", frame(), "before-notice", "review-worker · Completed", "new-value", "another-worker · Completed")
	m.CommitMessages()
	if m.conv.CommittedCount != len(m.conv.Messages) {
		t.Fatalf("committed %d of %d messages", m.conv.CommittedCount, len(m.conv.Messages))
	}
	check("committed", frame(), "before-notice", "review-worker · Completed", "new-value", "another-worker · Completed")
}

func TestCompletionNoticeKeepsItsRowDuringShellExecution(t *testing.T) {
	m := fixedComposerModel(100, 24)
	launch := core.ToolCall{ID: "launch", Name: tool.ToolAgent, Input: `{"description":"check file","prompt":"review it"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{launch}})
	m.conv.Append(core.ChatMessage{
		Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: launch.ID, ToolName: launch.Name},
		ToolDetails: toolresult.AgentDetails{TaskID: "worker"},
	})
	m.CommitMessages()

	shell := core.ToolCall{ID: "shell", Name: tool.ToolBash, Input: `{"command":"printf late-output"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{shell}})
	m.conv.Tool.Track([]core.ToolCall{shell})
	m.conv.Tool.MarkStarted(shell.ID)
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="check file">done</task-notification>`})
	rows := func(phase string) int {
		t.Helper()
		out := ansi.Strip(m.View().Content)
		launchRow, noticeRow, shellRow := -1, -1, -1
		for i, line := range strings.Split(out, "\n") {
			switch {
			case strings.Contains(line, "Started in background"):
				launchRow = i
			case strings.Contains(line, "check file · Completed"):
				noticeRow = i
			case strings.Contains(line, "printf late-output"):
				shellRow = i
			}
		}
		if launchRow < 0 || noticeRow != launchRow+2 || shellRow != noticeRow+2 {
			t.Fatalf("%s: want one blank row between launch, notice and shell; got rows %d, %d, %d:\n%s", phase, launchRow, noticeRow, shellRow, out)
		}
		return noticeRow
	}
	first := rows("running")
	m.CommitMessages()
	if settled := ansi.Strip(renderScrollbackLines(m.flush.visible)); !strings.Contains(settled, "check file · Completed") || strings.Contains(settled, "printf late-output") {
		t.Fatalf("notice must settle independently of the running command:\n%s", settled)
	}
	if row := rows("notice committed"); row != first {
		t.Fatalf("committing the notice moved it from row %d to %d", first, row)
	}
	m.conv.Tool.MarkComplete(shell.ID)
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{
		ToolCallID: shell.ID, ToolName: shell.Name, Content: ai.TextContent("late-output"),
	}})
	if row := rows("settled"); row != first {
		t.Fatalf("completion moved from row %d to %d", first, row)
	}
	m.CommitMessages()
	if row := rows("committed"); row != first {
		t.Fatalf("commit moved completion from row %d to %d", first, row)
	}
}

func TestLiveShellOutputHandsOffSettledNotice(t *testing.T) {
	m := fixedComposerModel(100, 12)
	m.env.BashPreviewLines = 20
	shell := core.ToolCall{ID: "shell", Name: tool.ToolBash, Input: `{"command":"long-running-command"}`}
	m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{shell}})
	m.conv.Tool.Track([]core.ToolCall{shell})
	m.showNoticeLine(mainNotice{FromAgent: true, Content: `<task-notification status="completed" description="worker">done</task-notification>`})
	m.CommitMessages()
	m.conv.Tool.SetProgress(shell.ID, "20 lines\n"+strings.Repeat("rolling output\n", 20))
	m.flushVisibleOverflow()

	var history strings.Builder
	for _, print := range m.flush.pendingPrints {
		history.WriteString(print.remaining)
		history.WriteString(renderScrollbackLines(print.rows))
	}
	if out := ansi.Strip(history.String()); strings.Count(out, "worker · Completed") != 1 || strings.Contains(out, "rolling output") {
		t.Fatalf("notice should enter scrollback without the live tool output:\n%s", out)
	}
}
