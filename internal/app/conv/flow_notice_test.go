package conv

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/genai-io/san/internal/app/kit"

	"github.com/charmbracelet/x/ansi"
)

func TestSchedulerDiagnosticPrecedesOptionalTitle(t *testing.T) {
	for _, status := range []string{"failed", "stopped"} {
		e := parseAgentEnvelope(fmt.Sprintf(`<task-notification scheduler="true" agent-name="%s" status="%s" duration-ms="1200">Error: provider unavailable</task-notification>`, strings.Repeat("Long title ", 20), status))
		for _, got := range []string{e.renderDisplayNotice(RenderContext{Width: 80}), e.renderWithIdentity(RenderContext{Width: 80})} {
			plain := ansi.Strip(got)
			if !strings.Contains(plain, "provider unavailable") || !strings.Contains(plain, backgroundStatusLabel(status)) || strings.Count(plain, "\n") != 1 || ansi.StringWidth(strings.TrimSuffix(plain, "\n")) > 80 {
				t.Fatalf("diagnostic lost behind title (%s): %q", status, plain)
			}
		}
	}
}

func TestSchedulerNoticeReceipt(t *testing.T) {
	oldProfile := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile = oldProfile })
	for _, status := range []string{"running", "completed", "failed", "stopped", "killed"} {
		var previous string
		for _, id := range []string{"purple-worker", "blue-worker"} {
			body := "Error: context canceled\nPartial output: repeated output"
			if status == "completed" {
				body = "## Conclusion\nFinal answer"
			}
			content := fmt.Sprintf(`<task-notification scheduler="true" task-id="%s" agent-name="Review" status="%s" duration-ms="1200">%s</task-notification>`, id, status, body)
			e := parseAgentEnvelope(content)
			got := e.renderDisplayNotice(RenderContext{Width: 80})
			if !strings.Contains(got, kit.FlowLabel("Review")) {
				t.Fatalf("missing neutral bold-Flow-only label: %q", got)
			}
			if previous != "" && previous != got {
				t.Fatalf("task ID changed Flow styling: %q / %q", previous, got)
			}
			previous = got
			plain := ansi.Strip(got)
			if status == "running" && (!strings.Contains(plain, " · Started") || strings.Contains(plain, "Running") || e.status != "running") {
				t.Fatalf("start receipt changed runtime status or used a live-state label: %q", plain)
			}
			if !strings.HasPrefix(plain, "● Flow · Review · ") || !strings.Contains(plain, " · 1s") || strings.Contains(plain, "repeated output") {
				t.Fatalf("bad receipt: %q", plain)
			}
			if strings.Contains(plain, "Final answer") != (status == "completed") {
				t.Fatalf("final result policy: %q", plain)
			}
			if strings.Contains(plain, "context canceled") != (status == "failed") {
				t.Fatalf("diagnostic policy: %q", plain)
			}
			if e.renderWithIdentity(RenderContext{Width: 80}) != got {
				t.Fatal("history differs")
			}
			for _, width := range []int{1, 8, 20, 40} {
				for line := range strings.SplitSeq(e.renderDisplayNotice(RenderContext{Width: width}), "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("width %d: %q", width, line)
					}
				}
			}
		}
	}
}

func TestSchedulerOutcomePriorityAndOptionalElapsed(t *testing.T) {
	for _, status := range []string{"completed", "interrupted", "timeout", "stopped"} {
		e := parseAgentEnvelope(fmt.Sprintf(`<task-notification scheduler="true" agent-name="Very long authored title" status="%s"></task-notification>`, status))
		got := ansi.Strip(e.renderDisplayNotice(RenderContext{Width: 100}))
		if strings.Contains(got, "0s") || !strings.Contains(got, backgroundStatusLabel(status)) {
			t.Fatalf("unexpected receipt: %q", got)
		}
		width := len(backgroundStatusLabel(status))
		heading, _, _ := strings.Cut(ansi.Strip(e.renderDisplayNotice(RenderContext{Width: width})), "\n")
		if strings.TrimSpace(heading) != backgroundStatusLabel(status) {
			t.Fatalf("outcome lost at width %d: %q", width, heading)
		}
	}
	e := parseAgentEnvelope(`<task-notification scheduler="true" agent-name="Review" status="failed">Error: provider unavailable</task-notification>`)
	got := ansi.Strip(e.renderDisplayNotice(RenderContext{Width: 100}))
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, " · provider unavailable") {
		t.Fatalf("diagnostic should be inline: %q", got)
	}
	e.attributed = false
	if e.isScheduler() {
		t.Fatal("unattributed envelope mistaken for scheduler")
	}
}

func TestSchedulerFinalResultShowsLeadingConclusion(t *testing.T) {
	result := "## Conclusion\n\n**Do not merge yet.**\n\n- Three issues need review.\n"
	for i := range 30 {
		result += fmt.Sprintf("\n- Supporting detail %02d.\n", i)
	}
	result += "\nTAIL_ONLY_DISCLAIMER"
	e := parseAgentEnvelope(`<task-notification scheduler="true" task-id="flow-final" agent-name="Review" status="completed">` + result + `</task-notification>`)
	for _, width := range []int{1, 20, 40, 80} {
		for _, budget := range []int{0, 1, 3, 50} {
			p := RenderContext{Width: width, AgentPreviewLines: budget, MDRenderer: NewMDRenderer(width)}
			got := e.renderDisplayNotice(p)
			plain := ansi.Strip(got)
			if width >= 40 && (!strings.Contains(plain, "Do not merge yet.") || !strings.Contains(plain, "/flow inspect flow-final") || strings.Contains(plain, "**Do not merge")) {
				t.Fatalf("leading Markdown conclusion lost at width %d budget %d: %q", width, budget, plain)
			}
			if strings.Contains(plain, "TAIL_ONLY_DISCLAIMER") {
				t.Fatalf("result showed the tail instead of the conclusion: %q", plain)
			}
			for line := range strings.SplitSeq(got, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("result overflows width %d: %q", width, line)
				}
			}
			if e.renderWithIdentity(p) != got {
				t.Fatal("restored final result differs from live result")
			}
		}
	}
}

func TestSchedulerFinalResultEmptyAndTruncated(t *testing.T) {
	for _, body := range []string{"", " \n\t"} {
		e := parseAgentEnvelope(`<task-notification scheduler="true" status="completed">` + body + `</task-notification>`)
		if got := ansi.Strip(e.renderDisplayNotice(RenderContext{Width: 80})); !strings.Contains(got, "No final result") {
			t.Fatalf("empty successful result was hidden: %q", got)
		}
	}
	e := parseAgentEnvelope(`<task-notification scheduler="true" task-id="flow-final" status="completed" result-truncated="true">Visible conclusion</task-notification>`)
	if got := ansi.Strip(e.renderDisplayNotice(RenderContext{Width: 80})); !strings.Contains(got, "Visible conclusion") || !strings.Contains(got, "/flow inspect flow-final") {
		t.Fatalf("serialized prefix lost its full-result hint: %q", got)
	}
}

func TestSchedulerRequiresExplicitAttribute(t *testing.T) {
	e := parseAgentEnvelope(`<task-notification agent-name="Flow" status="completed">answer</task-notification>`)
	if e.isScheduler() {
		t.Fatal("agent name mistaken for scheduler")
	}
	e = parseAgentEnvelope(`<task-notification not-scheduler="true" agent-name="Agent" status="completed">answer</task-notification>`)
	if e.isScheduler() {
		t.Fatal("non-scheduler attribute mistaken for scheduler")
	}
}
