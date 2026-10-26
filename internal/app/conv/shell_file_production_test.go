package conv

import (
	"encoding/json"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestShellCommandBlockProduction(t *testing.T) {
	for _, command := range []string{"echo first\n\n  echo second \\\n    third\n", strings.Repeat("long-command ", 15), strings.Repeat("你好世界", 20)} {
		input, _ := json.Marshal(map[string]string{"command": command})
		got := xansi.Strip(renderShellToolCall("Bash", string(input), 32, "●", ""))
		if strings.Count(got, "$ ") != 1 || strings.Contains(got, "┊") || strings.Contains(got, "…") {
			t.Fatalf("invalid command block: %q", got)
		}
		_, body, ok := strings.Cut(got, "\n")
		if !ok || body != xansi.Strip(kit.RenderCommandBlock(command, 32)) {
			t.Fatalf("command block differs from the lossless shared renderer: %q", got)
		}
		for _, row := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			if !strings.HasPrefix(row, "  ") || xansi.StringWidth(row) != 31 {
				t.Fatalf("command rectangle is not inset and uniformly padded: %q", row)
			}
		}
	}
	if got := xansi.Strip(renderShellToolCall("Bash", `{"command":"pwd"}`, 80, "●", "")); got != "● Bash(pwd)\n" {
		t.Fatalf("short: %q", got)
	}
}

func TestShellCommandSurfaceDoesNotColorOutput(t *testing.T) {
	call := core.ToolCall{ID: "shell", Name: "Bash", Input: `{"command":"first\nlast"}`}
	raw := RenderToolCalls(ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: 40, BashPreviewLines: 4,
		ResultMap: map[string]ToolResultData{call.ID: {ToolName: "Bash", Content: "output-marker", Details: toolresult.BashDetails{LineCount: 1}}},
	})
	rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(rows) != 4 || !strings.Contains(xansi.Strip(rows[3]), "output-marker") {
		t.Fatalf("command and output acquired extra vertical padding: %q", raw)
	}
	for _, i := range []int{0, 3} {
		if strings.Contains(rows[i], "48;") {
			t.Fatalf("command background escaped onto header/output: %q", rows[i])
		}
	}
	for _, row := range rows[1:3] {
		if !strings.HasPrefix(row, "  \x1b[") || xansi.StringWidth(row) != 39 {
			t.Fatalf("command is not one inset rectangle: %q", row)
		}
	}
}

func TestAgentBackgroundStartUsesResultConnector(t *testing.T) {
	for _, structured := range []bool{false, true} {
		for _, width := range []int{32, 80} {
			data := ToolResultData{ToolName: "Agent", Nested: true, Width: width, Content: "Agent started in background.\nTask ID: 87e5e853\nDescription: Fix approval continuity"}
			if structured {
				data.Details = toolresult.AgentDetails{TaskID: "87e5e853"}
			}
			got := xansi.Strip(RenderToolResultInline(data, nil))
			if !strings.HasPrefix(got, "  └ Started in background") || strings.ContainsAny(got, "✓✔") || strings.Contains(got, "Completed") {
				t.Fatalf("background handoff is disconnected or claims completion: %q", got)
			}
			var content strings.Builder
			for i, row := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				prefix := "    "
				if i == 0 {
					prefix = "  └ "
				}
				if !strings.HasPrefix(row, prefix) || xansi.StringWidth(row) > width {
					t.Fatalf("handoff continuation has wrong alignment: %q", row)
				}
				content.WriteString(strings.TrimPrefix(row, prefix))
			}
			if content.String() != "Started in background · 87e5e853" {
				t.Fatalf("handoff lost its exact task identity: %q", got)
			}
		}
	}
}

func TestShellFinishedTailProduction(t *testing.T) {
	data := ToolResultData{ToolName: "Bash", Nested: true, Width: 80, BashPreviewLines: 2, Content: "model-facing prefix", Details: toolresult.BashDetails{LineCount: 100, Tail: "98\n99\n100\n"}}
	got := xansi.Strip(RenderToolResultInline(data, nil))
	for _, want := range []string{"  │ 99", "  └ 100", "  │ … (98)\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %q", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "ctrl+o") {
		t.Fatal(got)
	}
	data.Details = toolresult.BashDetails{LineCount: 2, Tail: "one\ntwo\n"}
	data.Content = "one\ntwo\n"
	got = xansi.Strip(RenderToolResultInline(data, nil))
	if !strings.Contains(got, "one") || !strings.Contains(got, "two") || strings.Contains(got, "last ") {
		t.Fatalf("short output should remain complete: %q", got)
	}
	data.Details = toolresult.BashDetails{TaskID: "task-1"}
	got = xansi.Strip(RenderToolResultInline(data, nil))
	if !strings.Contains(got, "started in background") || strings.Contains(got, "completed") {
		t.Fatal(got)
	}
	data.IsError = true
	data.Content = "diagnostic\nError: exit code 1"
	data.Details = toolresult.BashDetails{LineCount: 100, Tail: "diagnostic\n", Error: "exit code 1"}
	got = xansi.Strip(RenderToolResultInline(data, nil))
	if !strings.Contains(got, "diagnostic") || !strings.Contains(got, "failed · exit code 1") {
		t.Fatal(got)
	}
}

func TestShellFinishedBlankTailDoesNotReviveEarlierOutput(t *testing.T) {
	const output = "first\nold\n\n\n"
	data := ToolResultData{ToolName: "Bash", Nested: true, Width: 80, BashPreviewLines: 1, Content: output,
		Details: toolresult.BashDetails{LineCount: 4, Tail: toolresult.LastLines(output, 20)}}
	got := xansi.Strip(RenderToolResultInline(data, nil))
	if strings.Contains(got, "old") || got != "  │ … (3)\n  └ \n" {
		t.Fatalf("blank last line revived earlier output or changed the shown count: %q", got)
	}
}

func TestFileDiffWithoutOuterRailProduction(t *testing.T) {
	for _, name := range []string{"Edit", "Write"} {
		data := ToolResultData{ToolName: name, Nested: true, Width: 80, Details: toolresult.FileChangeDetails{UnifiedDiff: "@@ -1 +1 @@\n-old\n+new\n", AddedLines: 1, RemovedLines: 1}}
		raw := RenderToolResultInline(data, nil)
		got := xansi.Strip(raw)
		rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
		if len(rows) != 3 || !strings.HasPrefix(rows[0], "  \x1b[") || !strings.HasPrefix(rows[1], "  \x1b[") || !strings.HasPrefix(xansi.Strip(rows[2]), "  └ ") {
			t.Fatalf("%s diff surface is not aligned with the result connector: %q", name, raw)
		}
		for _, want := range []string{"1 - old", "1 + new", "└ +1 -1"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s missing %q: %s", name, want, got)
			}
		}
		if strings.Contains(got, "┊") {
			t.Fatal(got)
		}
	}
}
