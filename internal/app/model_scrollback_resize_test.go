package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
)

func TestStreamingTableRenderRetriesAtResizeWidth(t *testing.T) {
	text := "| Command | 说明 |\n| --- | --- |\n| `printf very-long-command-argument` | 中文说明内容 |\n\n"
	m := fixedComposerModel(100, 40)
	row := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: text})
	m.conv.Stream.MessageID = row.ID
	m.conv.Stream.Active = true
	old := m.FlushStreamingBlocks()[0]().(flushResultMsg)
	resizeTerminal(m, 40, 40, tea.ResizeScrollbackMsg{})
	retry := m.handleFlushResult(old)
	if m.conv.Messages[0].ContentCommittedLen != 0 || len(m.flush.visible) != 0 {
		t.Fatal("old-width table committed after narrowing")
	}
	if retry == nil {
		t.Fatal("stale render was not retried")
	}
	// Widen again while the replacement render is in flight.
	narrow := retry().(flushResultMsg)
	resizeTerminal(m, 100, 40, tea.ResizeScrollbackMsg{})
	retry = m.handleFlushResult(narrow)
	if retry == nil || m.conv.Messages[0].ContentCommittedLen != 0 {
		t.Fatal("narrow render committed after widening")
	}
	wide := retry().(flushResultMsg)
	m.handleFlushResult(wide)
	if m.conv.Messages[0].ContentCommittedLen != len(text) {
		t.Fatal("replacement did not commit")
	}
	got := ansi.Strip(m.renderChatSection(""))
	if strings.Count(got, "Command") != 1 || !strings.Contains(got, "中文说明内容") {
		t.Fatalf("table content duplicated or lost: %q", got)
	}
	if got != ansi.Strip(old.printed) {
		t.Fatalf("wide/narrow/wide changed table layout:\n%s\nwant:\n%s", got, ansi.Strip(old.printed))
	}
}

func TestResizeDropsOnlyReportedWholeRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report tea.ResizeScrollbackMsg
		want   string
	}{
		{"unknown", tea.ResizeScrollbackMsg{Lines: []string{"same"}}, "same\nsame\nabcdefgh"},
		{"one repeated row", tea.ResizeScrollbackMsg{Known: true, Lines: []string{"same"}}, "same\nabcdefgh"},
		{"partial row omitted", tea.ResizeScrollbackMsg{Known: true, Lines: []string{"same", "same"}}, "abcdefgh"},
		{"partial text is not a whole row", tea.ResizeScrollbackMsg{Known: true, Lines: []string{"same", "same", "abcd"}}, "same\nsame\nabcdefgh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := flushState{
				visible:     scrollbackPhysicalLines("same\nsame\nabcdefgh", 0),
				resizedFrom: 8,
			}
			// A narrowing can push only part of abcdefgh into history. The
			// renderer cannot report that prefix; do not infer it or deduplicate
			// identical text that happens to follow a reported row.
			f.takeResizeScrollback(tc.report)
			if got := ansi.Strip(renderScrollbackLines(f.visible)); got != tc.want {
				t.Fatalf("retained rows = %q, want %q", got, tc.want)
			}
			if f.resizedFrom != 0 {
				t.Fatal("report did not release resize hold")
			}
		})
	}
}

func TestScrollbackPhysicalLinesExactWraps(t *testing.T) {
	for _, tc := range []struct {
		name, text  string
		width, rows int
	}{
		{"exact multiple", "abcdefgh", 4, 2},
		{"CJK odd width", strings.Repeat("中", 8), 3, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := scrollbackPhysicalLines(tc.text, tc.width)
			if len(rows) != tc.rows {
				t.Errorf("got %d physical rows, want %d", len(rows), tc.rows)
			}
			got := strings.ReplaceAll(ansi.Strip(renderScrollbackLines(rows)), "\n", "")
			if got != tc.text {
				t.Errorf("wrapped text = %q, want %q", got, tc.text)
			}
		})
	}
}
