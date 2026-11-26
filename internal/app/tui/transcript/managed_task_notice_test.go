package transcript

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestManagedTaskNoticePreview(t *testing.T) {
	content := `<task-notification task-id="managed-child" agent-id="native-session" status="completed" description="Check boundaries">` + "\n## Findings\n\n**Boundary** uses `guard` correctly.\n\n- Safe outcome\n</task-notification>"
	for _, budget := range []int{0, 1, 3} {
		p := RenderContext{Width: 60, AgentPreviewLines: budget, MDRenderer: NewMDRenderer(60), Messages: []core.ChatMessage{{Role: core.ChatNotice, AgentNotice: true, Content: content}}}
		got := RenderMessageAt(p, 0, false)
		want := renderAgentHeading("Agent: Check boundaries", p.Width, "●", backgroundIdentityColor("managed-child")) + "\n"
		if budget > 0 {
			want += renderAgentResponsePreview(parseAgentEnvelope(content).text(), p.Width, p.MDRenderer, budget)
		}
		if !strings.Contains(got, want) || strings.Contains(got, "<task-notification") || strings.Contains(ansi.Strip(got), "**Boundary**") {
			t.Fatalf("budget %d: %q, want %q", budget, got, want)
		}
		if budget == 0 && strings.Contains(got, "Safe outcome") {
			t.Fatalf("zero preview leaked answer: %q", got)
		}
		p.Messages[0].Role = core.ChatUser
		if user := RenderMessageAt(p, 0, false); strings.Contains(user, "Safe outcome") {
			t.Fatalf("ordinary envelope expanded: %q", user)
		}
	}
}

func TestManagedTaskNoticeOutcomes(t *testing.T) {
	for _, tc := range []struct{ status, body, label, finding string }{
		{"failed", "Error: provider unavailable\n\nPartial output:\nBoundary verified", "Failed", "Boundary verified"},
		{"stopped", "Error: context canceled", "Stopped", ""},
		{"stopped", "Error: context canceled\n\nPartial output:\nBoundary verified", "Stopped", "Boundary verified"},
		{"interrupted", "Error: connection lost\n\nPartial output:\nBoundary verified", "Interrupted", "Boundary verified"},
	} {
		content := `<task-notification task-id="managed-child" status="` + tc.status + `" description="Check">` + tc.body + `</task-notification>`
		p := RenderContext{Width: 80, AgentPreviewLines: 3, Messages: []core.ChatMessage{{Role: core.ChatNotice, AgentNotice: true, Content: content}}}
		got := ansi.Strip(RenderMessageAt(p, 0, false))
		if !strings.Contains(got, tc.label) || (tc.finding != "" && !strings.Contains(got, tc.finding)) || strings.Contains(got, "Finished") || strings.Contains(got, "context canceled") {
			t.Fatalf("%s: %q", tc.status, got)
		}
		if tc.status == "failed" && strings.Count(got, "provider unavailable") != 1 {
			t.Fatalf("duplicated diagnostic: %q", got)
		}
	}
}

func TestManagedTaskNoticeIdentity(t *testing.T) {
	old := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile = old })
	identity := AgentDisplay{ID: "native-session", Name: "reviewer", ColorKey: 2}
	for _, key := range []string{"managed-child", "native-session"} {
		p := RenderContext{Width: 80, AgentPreviewLines: 1, AgentIdentities: map[string]AgentDisplay{key: identity}}
		e := parseAgentEnvelope(`<task-notification task-id="managed-child" agent-id="native-session" status="completed" description="Check">Useful finding</task-notification>`)
		got := e.renderDisplayNotice(p)
		want := renderAgentHeading(AgentDisplayName(identity, p.AgentIdentities)+": Check", p.Width, "●", agentIdentityColor(identity, p.AgentColors)) + "\n"
		if !strings.HasPrefix(got, want) {
			t.Fatalf("%s identity/color mismatch: %q != %q", key, got, want)
		}
	}
}

func TestManagedAgentMatchesNativePresentation(t *testing.T) {
	call := core.ToolCall{ID: "native-call", Name: "Agent", Input: `{"name":"reviewer","description":"Check boundaries"}`}
	identity := AgentDisplay{ID: "managed-child", Name: "reviewer", ColorKey: 2}
	started := time.Now().Add(-12100 * time.Millisecond)
	activity := []string{"Model: internal-model", "Read(a.go)", "Read(b.go)", "Read(c.go)"}
	response := "## Findings\n\nBoundary verified."
	for _, width := range []int{20, 60, 80} {
		for _, budget := range []int{1, 3} {
			for _, status := range []string{"running", "completed"} {
				p := RenderContext{Width: width, AgentPreviewLines: budget, Frame: 3, MDRenderer: NewMDRenderer(width), AgentIdentities: map[string]AgentDisplay{call.ID: identity, "managed-child": identity}}
				v := AgentView{ID: "managed-child", Name: "reviewer", Description: "Check boundaries", StartedAt: started, Duration: 12 * time.Second, Activity: activity, Status: status, Output: response}
				native := ToolCallsParams{Width: width, AgentPreviewLines: budget, Frame: p.Frame, MDRenderer: p.MDRenderer, ToolCalls: []core.ToolCall{call}, CompactAgentLabel: true, AgentIdentities: p.AgentIdentities}
				if status == "running" {
					native.PendingCalls = []core.ToolCall{call}
					native.ToolStartedAt = map[string]time.Time{call.ID: started}
					native.TaskActivity = map[int][]string{0: activity}
				} else {
					native.ResultMap = map[string]ToolResultData{call.ID: {ToolName: "Agent", Content: response, Details: toolresult.AgentDetails{Response: &response, Duration: v.Duration}}}
				}
				if got, want := renderAgentView(v, p), RenderToolCalls(native); got != want {
					t.Fatalf("%s width=%d budget=%d does not match native Agent:\n%q\n%q", status, width, budget, got, want)
				}
			}
		}
	}
}

func TestManagedAgentApprovalAndHeightBudget(t *testing.T) {
	p := RenderContext{Width: 32, AgentPreviewLines: 3, Frame: 3}
	for _, name := range []string{"security", "performance", "correctness"} {
		p.ManagedAgents = append(p.ManagedAgents, AgentView{ID: name, Description: name, Status: "running", Activity: []string{"Read(very-long-file-name.go)", "Thinking..."}})
	}
	if rows := ActiveWorkerRows(p); rows != 3 {
		t.Fatalf("reserved rows=%d", rows)
	}
	for _, height := range []int{1, 3, 5, 12} {
		got, active := RenderActiveWorkers(p, height)
		if !active || strings.Count(got, "\n")+1 > height {
			t.Fatalf("height=%d overflow: %q", height, got)
		}
		for line := range strings.SplitSeq(got, "\n") {
			if ansi.StringWidth(line) > p.Width {
				t.Fatalf("width overflow: %q", line)
			}
		}
		if height >= 3 {
			for _, worker := range p.ManagedAgents {
				if !strings.Contains(got, worker.Description) {
					t.Fatalf("worker identity lost: %q", got)
				}
			}
		}
	}
	shell := core.ToolCall{ID: "shell", Name: "Bash", Input: `{"command":"pwd"}`}
	p.Messages = []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{shell}}}
	p.PendingCalls = []core.ToolCall{shell}
	p.ToolStartedAt = map[string]time.Time{shell.ID: time.Now()}
	if got, _ := RenderActiveWorkers(p, 4); ActiveWorkerRows(p) != 4 || !strings.Contains(ansi.Strip(got), "(pwd)") {
		t.Fatalf("managed workers hid an ordinary pending sibling: %q", got)
	}
	p.ManagedAgents[0].AwaitingApproval = true
	waiting := renderAgentView(p.ManagedAgents[0], p)
	if !strings.Contains(ansi.Strip(waiting), "Waiting for approval") || strings.Contains(waiting, "Thinking...") {
		t.Fatalf("approval shows stale activity: %q", waiting)
	}
}

func TestManagedTaskPlainNoticeUnchanged(t *testing.T) {
	for _, content := range []string{"Background job succeeded", "From @api: Check boundaries"} {
		p := RenderContext{Width: 80, AgentPreviewLines: 3, Messages: []core.ChatMessage{{Role: core.ChatNotice, AgentNotice: true, Content: content}}}
		if got := RenderMessageAt(p, 0, false); !strings.Contains(got, RenderAgentNotice(content, p.Width, p.MemberColors)) {
			t.Fatalf("plain notice changed: %q", got)
		}
	}
}
