package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/infra/proc"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestQueuedShellCallTitles(t *testing.T) {
	shells := proc.Shells()
	if len(shells) == 0 {
		t.Skip("no configured shell")
	}
	for _, shell := range shells {
		name, label := shell.Kind.ToolName(), shell.Label()
		t.Run(label, func(t *testing.T) {
			running := core.ToolCall{ID: "running", Name: name, Input: `{"command":"first"}`}
			queued := core.ToolCall{ID: "queued", Name: name, Input: `{"command":"second"}`}
			calls := []core.ToolCall{running, queued}
			p := ToolCallsParams{ToolCalls: calls, PendingCalls: calls, Width: 80,
				ToolProgress: map[string]ToolProgress{running.ID: {ShellLabel: label}}}
			got := stripANSI(RenderToolCalls(p))
			if !strings.Contains(got, label+"(first)") || !strings.Contains(got, label+"(second)") {
				t.Fatalf("running and queued commands should both show %q: %q", label, got)
			}
			p.ResultMap = map[string]ToolResultData{queued.ID: {Details: toolresult.BashDetails{}}}
			if got := shellCallTitle(queued, p); got != name {
				t.Fatalf("result without execution metadata: %q, want %q", got, name)
			}
			p.PendingCalls, p.ResultMap = nil, nil
			if got := shellCallTitle(queued, p); got != name {
				t.Fatalf("historical call without a result: %q, want %q", got, name)
			}
		})
	}
}

func TestShellCallRecordedTitles(t *testing.T) {
	for _, name := range []string{"Bash", "PowerShell"} {
		for _, label := range []string{"", "bash", "zsh", "pwsh", "PS"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				call := core.ToolCall{ID: "call", Name: name, Input: `{"command":"printf hello"}`}
				want := label
				if want == "" {
					want = name
				}
				params := ToolCallsParams{ToolProgress: map[string]ToolProgress{"call": {ShellLabel: label}}}
				if got := shellCallTitle(call, params); got != want {
					t.Fatalf("running title %q, want %q", got, want)
				}
				params.ResultMap = map[string]ToolResultData{"call": {Details: toolresult.BashDetails{ShellLabel: label}}}
				if got := shellCallTitle(call, params); got != want {
					t.Fatalf("recorded title %q, want %q", got, want)
				}
				params.ToolCalls = []core.ToolCall{call}
				params.Width = 40
				for _, expanded := range []bool{false, true} {
					params.ToolCallsExpanded = expanded
					if rendered := stripANSI(RenderToolCalls(params)); !strings.Contains(rendered, want) {
						t.Fatalf("recorded tool header missing %q: %q", want, rendered)
					}
				}
				params.ResultMap[call.ID] = ToolResultData{Details: toolresult.BashDetails{ShellLabel: label, TaskID: "task"}}
				if rendered := stripANSI(RenderToolCalls(params)); !strings.Contains(rendered, want) {
					t.Fatalf("background header missing %q: %q", want, rendered)
				}
				for _, details := range []any{nil, map[string]any{"shellLabel": "not typed"}, toolresult.BashDetails{}} {
					params.ResultMap = map[string]ToolResultData{"call": {Details: details}}
					if got := shellCallTitle(call, params); got != name {
						t.Fatalf("historical fallback %q, want %q", got, name)
					}
				}
				for _, width := range []int{12, 40, 80} {
					for _, rendered := range []string{renderShellToolCall(want, call.Input, width, "●", ""), renderBackgroundShellCall(want, call.Input, "task", width)} {
						if !strings.Contains(rendered, want) {
							t.Fatalf("missing title %q: %q", want, rendered)
						}
						for _, line := range strings.Split(rendered, "\n") {
							if lipgloss.Width(line) > width {
								t.Fatalf("width %d exceeded: %q", width, line)
							}
						}
					}
				}
			})
		}
	}
}
