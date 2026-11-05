package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBackgroundIdentityAndSeverity(t *testing.T) {
	oldProfile, wasDark := lipgloss.Writer.Profile, kit.IsDarkBackground()
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() {
		lipgloss.Writer.Profile = oldProfile
		if wasDark {
			kit.InitTheme("dark")
		} else {
			kit.InitTheme("light")
		}
	})
	for _, theme := range []string{"dark", "light"} {
		kit.InitTheme(theme)
		id := "task-a12"
		tone := backgroundIdentityColor(id)
		identity := lipgloss.NewStyle().Foreground(tone)
		call := core.ToolCall{ID: "launch", Name: "Bash", Input: `{"command":"go test ./...","description":"Run tests"}`}
		params := ToolCallsParams{Width: 100, ToolCalls: []core.ToolCall{call}, ResultMap: map[string]ToolResultData{call.ID: {ToolName: "Bash", Details: toolresult.BashDetails{TaskID: id}}}}
		for _, expanded := range []bool{false, true} {
			params.ToolCallsExpanded = expanded
			launch := RenderToolCalls(params)
			if !strings.Contains(launch, identity.Render("Bash · Run tests")) || !strings.Contains(ansi.Strip(launch), "go test ./...") {
				t.Fatalf("launch lost identity or command: %q", launch)
			}
		}
		for _, status := range []string{"completed", "failed", "interrupted", "timeout", "timed_out", "stopped", "killed", "unknown"} {
			wantColor := kit.CurrentTheme.Text
			switch status {
			case "completed":
				wantColor = kit.CurrentTheme.Success
			case "failed":
				wantColor = kit.CurrentTheme.Error
			case "interrupted", "timeout", "timed_out":
				wantColor = kit.CurrentTheme.Warning
			}
			wantStatus := lipgloss.NewStyle().Foreground(wantColor).Render(status)
			e := parseAgentEnvelope(`<task-notification task-id="task-a12" status="` + status + `" description="Run tests">Error: provider unavailable</task-notification>`)
			view := e.render(100, nil)
			if !strings.Contains(view, identity.Render("●")) || !strings.Contains(view, identity.Render("Run tests")) || !strings.Contains(view, wantStatus) {
				t.Fatalf("%s: identity/status lost: %q", status, view)
			}
			if strings.Contains(ansi.Strip(view), "provider unavailable") && !strings.Contains(view, toolCallStyle.Render(" · provider unavailable")) {
				t.Fatalf("reason must use normal text: %q", view)
			}
			// Parsing again models a persisted envelope rendered after resume.
			if view != parseAgentEnvelope(e.content).render(100, nil) {
				t.Fatal("unstable resumed color")
			}
			for _, width := range []int{1, 2, 8, 16, 32, 40} {
				e.description = "检查后台任务和权限"
				row := strings.TrimSuffix(e.render(width, nil), "\n")
				if ansi.StringWidth(row) > width || strings.Contains(row, "\n") {
					t.Fatalf("width %d: %q", width, row)
				}
				if width >= 32 && !strings.Contains(ansi.Strip(row), status) {
					t.Fatalf("status lost: %q", row)
				}
			}
		}
		unknown := RenderAgentNotice("failed is just a word in this report", 100, nil)
		if !strings.Contains(unknown, toolCallStyle.Render(" failed is just a word in this report")) {
			t.Fatalf("unstructured prose gained severity: %q", unknown)
		}
	}
}

func TestBackgroundShellCallDefaultWidth(t *testing.T) {
	input := `{"command":"go test ./...","description":"Run tests"}`
	want := renderBackgroundShellCall("Bash", input, "task-a12", 80)
	for _, width := range []int{0, -1} {
		if got := renderBackgroundShellCall("Bash", input, "task-a12", width); got != want {
			t.Fatalf("width %d should use the default, got %q", width, got)
		}
	}
}

func TestBackgroundNoticeKeepsConfiguredAgentColor(t *testing.T) {
	identity := AgentDisplay{ID: "agent-one", Name: "reviewer", ColorKey: 2}
	ctx := RenderContext{Width: 100, AgentIdentities: map[string]AgentDisplay{"task-one": identity}, AgentColors: map[string]string{"reviewer": "blue"}}
	e := parseAgentEnvelope(`<task-notification task-id="task-one" status="failed" description="Check">Error: denied</task-notification>`)
	got := e.renderWithIdentity(ctx)
	style := lipgloss.NewStyle().Foreground(agentIdentityColor(identity, ctx.AgentColors))
	if !strings.Contains(got, style.Render("●")) || !strings.Contains(got, style.Render("reviewer · Check")) {
		t.Fatalf("configured identity lost: %q", got)
	}
}
