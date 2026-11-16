package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
)

func TestManagedTaskNoticePreview(t *testing.T) {
	content := `<task-notification task-id="managed-child" agent-id="native-session" status="completed" description="Check boundaries">` + "\n## Findings\n\n**Boundary** uses `guard` correctly.\n\n- Safe outcome\n</task-notification>"
	for _, budget := range []int{0, 1, 3} {
		p := RenderContext{Width: 60, AgentPreviewLines: budget, MDRenderer: NewMDRenderer(60), Messages: []core.ChatMessage{{Role: core.ChatNotice, AgentNotice: true, Content: content}}}
		got := RenderMessageAt(p, 0, false)
		want := parseAgentEnvelope(content).renderWithIdentity(p)
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
		want := e.renderTaskNotice(p.Width, AgentDisplayName(identity, p.AgentIdentities), agentDescription(identity.Name, e.description), agentIdentityColor(identity, p.AgentColors))
		if !strings.HasPrefix(got, want) {
			t.Fatalf("%s identity/color mismatch: %q != %q", key, got, want)
		}
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
