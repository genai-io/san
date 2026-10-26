package conv

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestAgentIdentityColorAcrossLifecycle(t *testing.T) {
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
	identity := AgentDisplay{ID: "worker-route", Name: "reviewer", ColorKey: 1}
	identities := map[string]AgentDisplay{"spawn": identity, "worker-route": identity, "native-session": identity}
	for _, theme := range []string{"light", "dark"} {
		kit.InitTheme(theme)
		for _, width := range []int{32, 40, 100} {
			call := core.ToolCall{ID: "spawn", Name: "Agent", Input: `{"name":"reviewer","description":"Check"}`}
			params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, PendingCalls: []core.ToolCall{call}, Width: width,
				AgentIdentities: identities, ToolStartedAt: map[string]time.Time{call.ID: time.Now()}}
			header := renderAgentColoredLine("Agent · reviewer · Check", width, "●", memberPalette[1])
			live := RenderToolCalls(params)
			spinner := RenderPendingToolSpinner(PendingToolSpinnerParams{
				PendingCalls: params.PendingCalls, CurrentIdx: 0, Width: width,
				AgentIdentities: identities, ToolStartedAt: params.ToolStartedAt,
			})
			if spinner != live {
				t.Fatalf("pending spinner lost identity: %q != %q", spinner, live)
			}
			label := lipgloss.NewStyle().Foreground(memberPalette[1]).Render("Agent · reviewer · Check")
			if !strings.Contains(strings.SplitN(live, "\n", 2)[0], label) {
				t.Fatalf("%s width %d: live identity changed: %q", theme, width, live)
			}
			params.ResultMap = map[string]ToolResultData{call.ID: {ToolName: "Agent", Content: "Useful finding", Details: toolresult.AgentDetails{}}}
			finished := RenderToolCalls(params)
			if !strings.HasPrefix(finished, header+"\n") || !strings.Contains(finished, toolResultStyle.Render("  Useful finding")) {
				t.Fatalf("%s width %d: completion changed color or brightened body: %q", theme, width, finished)
			}
			input := `{"to":"worker-route","subject":"Check limits","message":"Verify limits"}`
			params.ToolCalls = []core.ToolCall{{ID: "mail", Name: "SendMessage", Input: input}}
			mail := RenderToolCalls(params)
			nameStyle := lipgloss.NewStyle().Foreground(memberPalette[1]).Bold(true)
			if strings.Contains(ansi.Strip(mail), "worker-route") || !strings.Contains(mail, nameStyle.Render("reviewer")) || params.ToolCalls[0].Input != input {
				t.Fatalf("%s width %d: message identity or routing input changed: %q", theme, width, mail)
			}
			ctx := RenderContext{Width: width, AgentIdentities: identities, Messages: []core.ChatMessage{
				{Role: core.ChatUser, Content: `<agent-message from="worker-route">Boundary safe</agent-message>`},
			}}
			received := strings.TrimPrefix(RenderMessageAt(ctx, 0, false), "\n")
			if !strings.Contains(received, nameStyle.Render("reviewer")) || !strings.Contains(ansi.Strip(received), "Boundary safe") {
				t.Fatalf("%s width %d: sender identity changed: %q", theme, width, received)
			}
			notice := renderAgentEnvelopeWithIdentity(`<task-notification task-id="worker-route" status="failed" description="Check limits">Error: provider unavailable</task-notification>`, ctx)
			identityStyle := lipgloss.NewStyle().Foreground(memberPalette[1])
			if !strings.Contains(notice, identityStyle.Render("◆ reviewer")) || !strings.Contains(ansi.Strip(notice), "failed") {
				t.Fatalf("%s width %d: failure changed identity or lost outcome: %q", theme, width, notice)
			}
			_, outcome, _ := strings.Cut(notice, toolResultStyle.Render(" · "))
			outcome = strings.TrimSuffix(outcome, "\n")
			if outcome != errorStyle.Render(ansi.Strip(outcome)) {
				t.Fatalf("failure lost its separate error color: %q", notice)
			}
			for _, view := range []string{mail, received, notice} {
				if strings.Count(strings.TrimSuffix(view, "\n"), "\n") != 0 || ansi.StringWidth(strings.TrimSuffix(view, "\n")) > width {
					t.Fatalf("identity message exceeds one bounded row: %q", view)
				}
			}
		}
	}
}

func TestAgentIdentityDuplicateAndUnknownNames(t *testing.T) {
	first := AgentDisplay{ID: "worker-one", Name: "reviewer", ColorKey: 0}
	identities := map[string]AgentDisplay{"worker-one": first, "spawn-one": first}
	if got := AgentDisplayName(first, identities); got != "reviewer" {
		t.Fatalf("aliases falsely made a duplicate name: %q", got)
	}
	identities["worker-two"] = AgentDisplay{ID: "worker-two", Name: "reviewer", ColorKey: 1}
	if got := AgentDisplayName(first, identities); got != "reviewer · worker-one" {
		t.Fatalf("duplicate name lacks stable ID: %q", got)
	}
	id := "unresolved-agent-123456789abcdef"
	got := ansi.Strip(renderAgentMessageLine("●", "To", id, "Check", 32, nil, nil, nil))
	if strings.Contains(got, id) || !strings.Contains(got, shortAgentDisplayID(id)) || ansi.StringWidth(got) > 32 {
		t.Fatalf("unknown recipient lacks bounded ID fallback: %q", got)
	}
}
