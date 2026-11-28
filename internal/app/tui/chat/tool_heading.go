package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// settledToolHeading folds compact results into their call header. Judged and
// expanded calls retain the separate result so the evidence stays in order.
func settledToolHeading(tc core.ToolCall, data ToolResultData, width int) (row string, ok bool) {
	if data.Expanded || data.Decision != nil {
		return "", false
	}
	switch tc.Name {
	case tool.ToolBash, tool.ToolPowerShell, tool.ToolAgent, tool.ToolSendMessage, tool.ToolAskUserQuestion:
		return "", false
	case tool.ToolEdit, tool.ToolWrite:
		details, ok := data.Details.(toolresult.FileChangeDetails)
		if !ok || data.IsError {
			return "", false
		}
		label := compactToolLabel(tc)
		detail := optionalToolDetail(label, fileChangeSummary(details), width)
		return renderCompactToolHeading(label, detail, width), true
	}

	if data.IsError {
		return renderSettledToolFailure(tc, withoutRecall(data.Content), width), true
	}
	if tc.Name == tool.ToolRead {
		label := compactToolLabel(tc)
		detail := optionalToolDetail(label, formatReadResultSummary(withoutRecall(data.Content)), width)
		return renderCompactToolHeading(label, detail, width), true
	}
	return "", false
}

func renderSettledToolFailure(tc core.ToolCall, content string, width int) string {
	if width <= 0 {
		width = 80
	}
	label := compactToolLabel(tc)
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(xansi.Strip(content)), "Error: "))
	if !strings.Contains(content, "\n") {
		// Add a diagnostic inline only when the entire heading still fits.
		if detail, note := toolStatusDetail(label, "Failed", diagnosticLine(content), errorStyle, width); detail != "" && note == "" {
			return renderCompactToolHeading(label, detail, width)
		}
	}
	var row string
	if detail, _ := toolStatusDetail(tc.Name, "Failed", "", errorStyle, width); detail != "" {
		row = renderCompactToolHeading(label, detail, width)
	} else {
		row = renderCompactToolHeading(label, "", width) + errorStyle.Render(widgets.TruncateText("  Failed", width)) + "\n"
	}
	return row + renderDiagnosticPreview(content, width)
}

// Budget terminal rows rather than source lines so one long diagnostic cannot
// fill the conversation. The transcript's expanded entry retains the original.
func renderDiagnosticPreview(content string, width int) string {
	if content == "" {
		return ""
	}
	const maxRows = 3
	prefix := nestedBodyPrefix
	if width <= lipgloss.Width(prefix) {
		prefix = ""
	}
	room := max(1, width-lipgloss.Width(prefix))
	var out strings.Builder
	rows := 0
	for line := range strings.SplitSeq(content, "\n") {
		for segment := range strings.SplitSeq(xansi.Wrap(diagnosticLine(line), room, ""), "\n") {
			if rows == maxRows {
				out.WriteString(toolResultStyle.Render(prefix+widgets.TruncateText("… more diagnostics", room)) + "\n")
				return out.String()
			}
			out.WriteString(toolResultStyle.Render(prefix+segment) + "\n")
			rows++
		}
	}
	return out.String()
}

func diagnosticLine(line string) string {
	return strings.ReplaceAll(visibleLine(line), "\t", "    ")
}

func compactToolLabel(tc core.ToolCall) string {
	if args := extractToolArgs(tc.Input); args != "" {
		args = strings.NewReplacer("\n", "␊", "\r", "␍", "\t", "␉").Replace(xansi.Strip(args))
		return tc.Name + " " + args
	}
	return tc.Name
}

func renderCompactToolHeading(label, detail string, width int) string {
	if width > 0 {
		label = widgets.TruncateText(label, max(1, width-widgets.ToolIconWidth-lipgloss.Width(detail)))
	}
	return toolCallStyle.Width(widgets.ToolIconWidth).Render("●") + toolCallStyle.Render(label) + detail + "\n"
}

func optionalToolDetail(label, summary string, width int) string {
	detail := " · " + summary
	if width > 0 && lipgloss.Width("● "+label+detail) > width {
		return ""
	}
	return toolResultStyle.Render(detail)
}

// Reserve room for identity and status before trying to fit the diagnostic.
// On narrower terminals move the reason below the heading instead of losing it.
func toolStatusDetail(name, status, reason string, style lipgloss.Style, width int) (detail, note string) {
	prefix := " · "
	minimum := lipgloss.Width("● " + name)
	if width > 0 && minimum+lipgloss.Width(prefix+status) > width {
		return "", ""
	}
	if reason != "" {
		if width <= 0 || minimum+lipgloss.Width(prefix+status+": "+reason) <= width {
			return toolResultStyle.Render(prefix) + style.Render(status) + toolResultStyle.Render(": "+reason), ""
		}
		note = widgets.TruncateText(reason, max(1, width-2))
	}
	return toolResultStyle.Render(prefix) + style.Render(status), note
}

func toolHeadingWidth(width int, detail string) int {
	if width > 0 {
		return max(1, width-lipgloss.Width(detail))
	}
	return width
}

func compactAgentStatus(data ToolResultData, details toolresult.AgentDetails) (status, reason string, style lipgloss.Style) {
	style = toolResultStyle
	switch {
	case details.Stopped:
		status = "Stopped"
	case data.IsError:
		status, style = "Failed", errorStyle
	default:
		return "Finished", "", style
	}
	reason = details.Error
	if reason == "" {
		reason = withoutRecall(data.Content)
	}
	if details.Stopped && widgets.IsRoutineStopReason(reason) {
		reason = ""
	}
	return status, reason, style
}
