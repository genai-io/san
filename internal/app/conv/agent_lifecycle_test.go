package conv

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestAgentLifecycleCompactResponse(t *testing.T) {
	for _, width := range []int{32, 40} {
		data := ToolResultData{Content: "Actual useful finding\n\nAgentID: child\n\nTranscript: /tmp/child.jsonl", Width: width}
		got := ansi.Strip(renderAgentResultDetails(data, toolresult.AgentDetails{}, nil))
		if !strings.Contains(got, "Actual useful finding") || strings.Contains(got, "Transcript:") || strings.Contains(got, "Finished") {
			t.Fatalf("unexpected response: %q", got)
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("line exceeds %d: %q", width, line)
			}
		}
	}
}

func TestAgentLifecycleResponseWrapsWords(t *testing.T) {
	const response = "Finding: authentication uses the expected permission boundary. Validation: foreground checks passed. Next: no changes required."
	for _, width := range []int{32, 40, 100} {
		got := ansi.Strip(renderCompactAgentResponse(response, width))
		if strings.Join(strings.Fields(got), " ") != response {
			t.Fatalf("width %d split response words: %q", width, got)
		}
		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if !strings.HasPrefix(line, "  ") || ansi.StringWidth(line) > width {
				t.Fatalf("width %d lost response indentation or overflowed: %q", width, line)
			}
		}
	}
}

func TestAgentLifecyclePreservesTranscriptProse(t *testing.T) {
	for _, prose := range []string{
		"Finding\n\nTranscript: this paragraph is a finding",
		"Finding\n\nAgentID: worker\n\nTranscript: /tmp/unrelated.jsonl",
	} {
		got := ansi.Strip(renderCompactAgentResponse(prose, 80))
		if !strings.Contains(got, "Transcript:") {
			t.Fatalf("worker prose was stripped: %q", got)
		}
	}
}

func TestAgentLifecycleFailureBudgetAndStoppedEvidence(t *testing.T) {
	for _, width := range []int{32, 40} {
		for _, stopped := range []bool{false, true} {
			data := ToolResultData{Width: width, IsError: !stopped}
			details := toolresult.AgentDetails{Stopped: stopped, Error: "provider unavailable: " + strings.Repeat("diagnostic ", 30)}
			got := ansi.Strip(renderAgentResultDetails(data, details, nil))
			if !strings.Contains(got, "provider") || strings.Count(got, "\n") != 1 {
				t.Fatalf("missing bounded reason: %q", got)
			}
			if ansi.StringWidth(strings.TrimSuffix(got, "\n")) > width {
				t.Fatalf("failure exceeds %d: %q", width, got)
			}
			data.Expanded = true
			if expanded := ansi.Strip(renderAgentResultDetails(data, details, nil)); stopped && !strings.Contains(expanded, "Stopped") {
				t.Fatalf("expansion lost stopped state: %q", expanded)
			}
		}
	}
}

func TestAgentLifecycleElapsedSharesLatestActivity(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"description":"Review"}`}
	params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call},
		ToolStartedAt: map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)},
		TaskActivity:  map[int][]string{0: {"Read(main.go)"}}, Width: 40}
	got := ansi.Strip(RenderToolCalls(params))
	if !strings.Contains(got, "  Read(main.go) · 12s\n") || strings.Count(got, "\n") != 2 {
		t.Fatalf("timer should share the activity row: %q", got)
	}
	params.CurrentIdx = 1
	if got := ansi.Strip(RenderToolCalls(params)); !strings.Contains(got, "Pending") || strings.Contains(got, "Read(") {
		t.Fatalf("ineligible worker advertised execution: %q", got)
	}
}

func TestAgentLifecycleShortLayoutKeepsExecutionActivity(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"description":"Security review"}`}
	params := RenderContext{Width: 40, StreamActive: true,
		Messages:     []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}}},
		PendingCalls: []core.ToolCall{call}, ToolStartedAt: map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)},
		TaskActivity: map[int][]string{0: {"Read(main.go)"}},
	}
	view, active := RenderActiveWorkers(params, 3)
	plain := ansi.Strip(view)
	if !active || !strings.Contains(plain, "Read(main.go)") || !strings.Contains(plain, "12s") || strings.Contains(plain, "Pending") {
		t.Fatalf("short layout lost the same worker's running state: %q", plain)
	}
}

func TestAgentLifecycleShortLayoutKeepsStateAfterLongIdentity(t *testing.T) {
	for _, width := range []int{32, 40} {
		call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"name":"reviewer","description":"Review execution and approval consistency"}`}
		params := RenderContext{Width: width, StreamActive: true,
			Messages:     []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}}},
			PendingCalls: []core.ToolCall{call}, ToolStartedAt: map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)},
			TaskActivity: map[int][]string{0: {"Read(main.go)"}},
		}
		for _, waiting := range []bool{false, true} {
			state := "Read(main.go) · 12s"
			if waiting {
				params.AwaitingApprovalID, params.DockedModalActive = call.ID, true
				state = "Waiting for approval"
			}
			view, active := RenderActiveWorkers(params, 2)
			plain := ansi.Strip(view)
			lines := strings.Split(plain, "\n")
			if !active || len(lines) != 2 || !strings.HasPrefix(lines[0], "● Agent · reviewer · ") || !strings.HasSuffix(lines[0], "…") || lines[1] != "  "+state {
				t.Fatalf("width %d: wrapped identity displaced current state: %q", width, plain)
			}
		}
	}
}

func TestAgentLifecycleLatestActivity(t *testing.T) {
	got := ansi.Strip(renderAgentActivityCompact([]string{"Read(old.go)", "Thinking..."}, 40))
	if strings.Contains(got, "old.go") || got != "  Thinking...\n" {
		t.Fatalf("stale activity: %q", got)
	}
}

func TestAgentLifecycleDoesNotPromoteOutputCountersToActivity(t *testing.T) {
	for _, counter := range []string{"1 line", "12 lines", "1.2k lines\ncommand output"} {
		got := ansi.Strip(renderAgentActivityCompact([]string{"Bash(check)", counter}, 40))
		if got != "  Working...\n" {
			t.Fatalf("output bookkeeping replaced meaningful Agent state: %q", got)
		}
	}
}

func TestAgentLifecycleIdentityAndStateTransitions(t *testing.T) {
	for _, width := range []int{32, 80} {
		for _, input := range []string{
			`{"name":"reviewer","description":"Security review"}`,
			`{"name":"审查员","description":"检查执行链路"}`,
		} {
			call := core.ToolCall{ID: "worker", Name: "Agent", Input: input}
			params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, Width: width}
			header := ansi.Strip(renderAgentToolLine(formatAgentLabel(parseAgentInput(input)), width, "●", "")) + "\n"
			check := func(state string, absent ...string) {
				t.Helper()
				got := ansi.Strip(RenderToolCalls(params))
				if !strings.HasPrefix(got, header) || !strings.Contains(got, "  "+state+"\n") {
					t.Fatalf("width %d: identity or state changed for %q: %q", width, state, got)
				}
				for _, text := range absent {
					if strings.Contains(got, text) {
						t.Fatalf("width %d: %q retained stale %q: %q", width, state, text, got)
					}
				}
				for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n")[1:] {
					if !strings.HasPrefix(line, "  ") || ansi.StringWidth(line) > width {
						t.Fatalf("width %d: inconsistent indentation: %q", width, line)
					}
				}
			}
			check("Pending")
			params.ToolStartedAt = map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)}
			check("Starting... · 12s", "Pending")
			params.TaskActivity = map[int][]string{0: {"Read(main.go)"}}
			check("Read(main.go) · 12s", "Starting...")
			params.AwaitingApprovalID, params.DockedModalActive = call.ID, true
			check("Waiting for approval", "Read(main.go)", "12s")
			params.AwaitingApprovalID, params.DockedModalActive = "", false
			params.TaskActivity[0] = append(params.TaskActivity[0], "Thinking...")
			check("Thinking... · 12s", "Waiting for approval", "Read(main.go)")
			params.ResultMap = map[string]ToolResultData{call.ID: {
				ToolName: "Agent", Content: "No vulnerabilities found", Details: toolresult.AgentDetails{},
			}}
			check("No vulnerabilities found", "Thinking...", "12s", "Finished", "Completed")
		}
	}
}

func TestAgentLifecycleFallbackMatchesInline(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent", Input: `{"name":"reviewer","description":"Security review"}`}
	for _, width := range []int{32, 80} {
		for _, state := range []struct {
			name     string
			started  bool
			activity []string
		}{
			{name: "pending"},
			{name: "pending ignores stale activity", activity: []string{"Read(stale.go)"}},
			{name: "starting", started: true},
			{name: "running", started: true, activity: []string{"Read(main.go)"}},
			{name: "long current activity", started: true, activity: []string{"Read(" + strings.Repeat("long-path/", 10) + "main.go)"}},
		} {
			t.Run(state.name+"/"+strconv.Itoa(width), func(t *testing.T) {
				params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, Width: width,
					TaskActivity: map[int][]string{0: state.activity}}
				if state.started {
					params.ToolStartedAt = map[string]time.Time{call.ID: time.Now().Add(-12 * time.Second)}
				}
				inline := ansi.Strip(RenderToolCalls(params))
				fallback := ansi.Strip(RenderPendingToolSpinner(PendingToolSpinnerParams{
					PendingCalls: params.PendingCalls, ToolStartedAt: params.ToolStartedAt,
					TaskActivity: params.TaskActivity, Width: width,
				}))
				if fallback != inline {
					t.Fatalf("fallback changed Agent identity or state:\ninline:   %q\nfallback: %q", inline, fallback)
				}
			})
		}
	}
}

func TestAgentLifecycleStoppedWithoutError(t *testing.T) {
	got := ansi.Strip(renderAgentResultDetails(ToolResultData{Content: "context canceled", Width: 40}, toolresult.AgentDetails{Stopped: true}, nil))
	if got != "  Stopped\n" {
		t.Fatalf("unexpected stopped result: %q", got)
	}
}

func TestAgentLifecycleBuildingKeepsIdentity(t *testing.T) {
	got := ansi.Strip(RenderPendingToolSpinner(PendingToolSpinnerParams{BuildingTool: "Agent", Width: 32}))
	if got != "● Agent\n  Pending\n" {
		t.Fatalf("streamed Agent call lost its identity: %q", got)
	}
}

func TestAgentLifecycleNegativeCurrentIndex(t *testing.T) {
	call := core.ToolCall{ID: "worker", Name: "Agent"}
	if got := RenderPendingToolSpinner(PendingToolSpinnerParams{PendingCalls: []core.ToolCall{call}, CurrentIdx: -1}); got != "" {
		t.Fatalf("invalid selection rendered a worker: %q", got)
	}
	if got := ansi.Strip(RenderPendingToolSpinner(PendingToolSpinnerParams{BuildingTool: "Agent", PendingCalls: []core.ToolCall{call}, CurrentIdx: -1, Width: 32})); got != "● Agent\n  Pending\n" {
		t.Fatalf("building fallback lost identity: %q", got)
	}
}
