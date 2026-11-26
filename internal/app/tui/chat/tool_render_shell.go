package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// renderShellToolCall renders a shell tool call without ever truncating its
// command. A single-line command stays in Bash(command) form only when that
// complete label fits; its optional description may be shortened to keep the
// row within the terminal. Commands that contain a newline or do not fit become
// a full command block below the Bash header and soft-wrap without ellipses.
func renderShellToolCall(toolName, input string, width int, icon, detail string) string {
	command, description := extractBashCommand(input)

	if strings.TrimSpace(command) != "" && !strings.Contains(command, "\n") {
		labelWidth := max(1, bashPreviewLabelWidth(width)-lipgloss.Width(detail))
		commandLabel := fmt.Sprintf("%s(%s)", toolName, command)
		if lipgloss.Width(commandLabel) <= labelWidth {
			label := toolCallStyle.Render(commandLabel)
			label += renderBashDescription(description, labelWidth-lipgloss.Width(commandLabel))
			iconCell := toolCallStyle.Width(widgets.ToolIconWidth).Render(icon)
			return lipgloss.JoinHorizontal(lipgloss.Top, iconCell, label) + detail + "\n"
		}
	}
	return renderShellCommandBlock(toolName, command, description, width, icon, detail)
}

func renderShellCommandBlock(toolName, command, description string, width int, icon, detail string) string {
	if strings.TrimSpace(command) == "" {
		command = "(no command)"
	}
	labelWidth := max(1, bashPreviewLabelWidth(width)-lipgloss.Width(detail))
	var sb strings.Builder

	displayName := xansi.Truncate(toolName, labelWidth, "…")
	header := toolCallStyle.Render(displayName) + renderBashDescription(description, labelWidth-lipgloss.Width(displayName))
	iconCell := toolCallStyle.Width(widgets.ToolIconWidth).Render(icon)
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, iconCell, header) + detail + "\n")

	sb.WriteString(widgets.RenderCommandBlock(command, width))
	return sb.String()
}

// extractBashCommand pulls the command and optional description out of a Bash
// tool call's raw JSON input.
func extractBashCommand(input string) (command, description string) {
	var params struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", ""
	}
	return params.Command, strings.Join(strings.Fields(params.Description), " ")
}

// renderBashDescription dims the description in parentheses after the Bash
// label, shortening the text — never the closing paren — to fit width.
func renderBashDescription(description string, width int) string {
	inner := width - lipgloss.Width(" ()")
	if description == "" || inner < 8 { // too narrow for words: "()" or "(...)"
		return ""
	}
	return toolResultStyle.Render(" (" + xansi.Truncate(description, inner, "...") + ")")
}

func bashPreviewLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	return max(1, width-widgets.ToolIconWidth)
}

func renderBashToolResultInline(data ToolResultData) string {
	content := data.Content
	details, hasDetails := data.Details.(toolresult.BashDetails)
	if details.TaskID != "" {
		body := ""
		if data.Expanded {
			body = renderBashOutput(content, data.Width, toolResultStyle, false)
		}
		return body + renderBashOutput("started in background · "+details.TaskID, data.Width, toolCallStyle, true)
	}

	summary := ""
	if data.IsError {
		content, summary = formatBashFailure(content, data.Details)
	}
	tail := content
	if hasDetails && details.Tail != "" {
		tail = details.Tail
	}
	if data.Expanded {
		body := renderBashOutput(content, data.Width, toolResultStyle, !data.IsError)
		if data.IsError {
			return body + renderBashOutput(summary, data.Width, errorStyle, true)
		}
		if body == "" {
			return renderBashOutput("no output", data.Width, toolResultStyle, true)
		}
		return body
	}
	if data.IsError {
		// Error evidence keeps its diagnostic budget and wrapping; the failure
		// footer, not the last diagnostic, closes the result.
		preview := toolresult.LastLines(tail, setting.MaxBashPreviewLines)
		return renderBashOmission(tail, preview, data.Width, details) +
			renderBashOutput(preview, data.Width, toolResultStyle, false) +
			renderBashOutput(compactFailureReason(summary, data.Width), data.Width, errorStyle, true)
	}
	if hasDetails && details.LineCount == 0 || tail == "" {
		return renderBashOutput("no output", data.Width, toolResultStyle, true)
	}
	if data.BashPreviewLines <= 0 {
		return renderBashOutput("completed", data.Width, toolResultStyle, true)
	}
	return renderOutputPreview(tail, data.BashPreviewLines, data.Width, details, true)
}

// renderOutputPreview keeps the last n actual output rows. Only a completed
// success closes its last row; live previews retain an open guide.
func renderOutputPreview(output string, n, width int, details toolresult.BashDetails, closed bool) string {
	if n <= 0 || output == "" {
		return ""
	}
	preview := toolresult.LastLines(output, n)
	var sb strings.Builder
	sb.WriteString(renderBashOmission(output, preview, width, details))
	lines := strings.Split(strings.TrimSuffix(preview, "\n"), "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(xansi.Strip(visibleLine(line)), "\t", "    ")
		marker := "│"
		if closed && i == len(lines)-1 {
			marker = "└"
		}
		sb.WriteString(renderBashRow(marker, widgets.TruncateText(line, bashOutputRoom(width)), toolResultStyle, width))
	}
	return sb.String()
}

func formatBashFailure(content string, details any) (string, string) {
	if bashDetails, ok := details.(toolresult.BashDetails); ok {
		content = trimBashErrorSuffix(content, bashDetails.Error)
		return content, formatBashFailureSummary(bashDetails.Error)
	}
	if output, reason, ok := splitBashFailureContent(content); ok {
		return output, formatBashFailureSummary(reason)
	}
	return content, "failed"
}

func formatBashFailureSummary(reason string) string {
	summary := "failed"
	if reason = formatBashFailureReason(reason); reason != "" {
		summary += " · " + reason
	}
	return summary
}

func trimBashErrorSuffix(content, errorMessage string) string {
	withoutTerminator := strings.TrimSuffix(content, "\n")
	if withoutTerminator == "Error: "+errorMessage {
		return ""
	}
	if output, ok := strings.CutSuffix(withoutTerminator, "\nError: "+errorMessage); ok {
		return output
	}
	return content
}

func formatBashFailureReason(reason string) string {
	const timeoutPrefix = "command timed out after "
	if after, ok := strings.CutPrefix(reason, timeoutPrefix); ok {
		duration, _, _ := strings.Cut(after, " — ")
		return "timed out after " + duration
	}
	return reason
}

// renderBashRow keeps the guide quieter than its content and aligned with "$".
func renderBashRow(marker, content string, style lipgloss.Style, width int) string {
	return strings.Repeat(" ", widgets.CommandMarkerColumn(width)) +
		lipgloss.NewStyle().Foreground(widgets.CurrentTheme.TextDisabled).Render(marker) + " " + style.Render(content) + "\n"
}

func bashOutputRoom(width int) int {
	if width <= 0 {
		width = 80
	}
	return max(1, width-1-widgets.CommandMarkerColumn(width)-2)
}

func bashOutputLineCount(output string) int {
	if output == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(output, "\n"), "\n") + 1
}

// renderBashOutput wraps full output without discarding blank lines. A settled
// result closes on its last physical row; a running body keeps the guide open.
func renderBashOutput(content string, width int, style lipgloss.Style, closed bool) string {
	if content == "" {
		return ""
	}
	rows := strings.Split(xansi.Hardwrap(strings.ReplaceAll(visibleBashOutput(content), "\t", "    "), bashOutputRoom(width), true), "\n")
	var sb strings.Builder
	for i, row := range rows {
		marker := "│"
		if closed && i == len(rows)-1 {
			marker = "└"
		}
		sb.WriteString(renderBashRow(marker, row, style, width))
	}
	return sb.String()
}

func visibleBashOutput(output string) string {
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	for i, line := range lines {
		lines[i] = visibleLine(line)
	}
	return strings.Join(lines, "\n")
}

// Counts refer only to whole omitted output rows. A byte-capped partial line
// still needs an omission marker, but must not invent an omitted-line count.
func renderBashOmission(output, preview string, width int, details toolresult.BashDetails) string {
	shown := bashOutputLineCount(preview)
	if bashOutputLineCount(output) <= shown && details.LineCount <= shown && !details.TailTruncated {
		return ""
	}
	label := "…"
	if omitted := details.LineCount - shown; omitted > 0 {
		counted := fmt.Sprintf("… (%d)", omitted)
		if lipgloss.Width(counted) <= bashOutputRoom(width) {
			label = counted
		}
	}
	return renderBashRow("│", label, lipgloss.NewStyle().Foreground(widgets.CurrentTheme.TextDisabled), width)
}
