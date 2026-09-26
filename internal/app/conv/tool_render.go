package conv

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

var (
	headerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(kit.CurrentTheme.Border).
			Padding(0, 1)

	truncatedStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Error)
)

// RenderToolResultInline renders a tool result inline (without leading newline).
func RenderToolResultInline(data ToolResultData, mdRenderer *MDRenderer) string {
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}

	// File-change failures retain their requested-change preview, and Bash
	// failures retain their line-count summary. Every other nested failure can
	// use the shared layout without repeating the call's tool name.
	if data.Nested && data.IsError {
		switch toolName {
		case tool.ToolBash, tool.ToolPowerShell, tool.ToolEdit, tool.ToolWrite:
		default:
			return renderNestedFailure(data.Content, data.Width)
		}
	}

	switch toolName {
	case tool.ToolBash, tool.ToolPowerShell:
		if data.Nested {
			return renderBashToolResultInline(data)
		}
		return renderGenericToolResultInline(data)
	case tool.ToolSkill:
		return renderSkillResultInline(data)
	case tool.ToolAgent, tool.ToolSendMessage:
		return renderTaskResultInline(data, mdRenderer)
	case tool.ToolEdit, tool.ToolWrite:
		if data.Nested {
			return renderNestedFileChangeResultInline(data)
		}
		return renderFileChangeResultInline(data)
	case tool.ToolRead:
		if data.Nested {
			return renderNestedReadResultInline(data)
		}
		return renderGenericToolResultInline(data)
	case tool.ToolAskUserQuestion:
		return renderAskUserResultInline(data)
	}
	if data.Nested {
		return renderNestedGenericToolResultInline(data)
	}
	return renderGenericToolResultInline(data)
}

const (
	// Nested markers share a display column across command, body, and trailer rows.
	nestedBodyPrefix    = "  ┊ "
	nestedTrailerPrefix = "  └ "
	bashPrompt          = "  $ "
)

func renderNestedReadResultInline(data ToolResultData) string {
	if data.IsError {
		return renderNestedFailure(data.Content, data.Width)
	}

	content := strings.TrimSuffix(data.Content, "\n")
	var sb strings.Builder
	if data.Expanded {
		sb.WriteString(renderNestedToolBody(content, data.Width))
	}
	sb.WriteString(renderNestedToolTrailer(formatReadResultSummary(content), toolResultStyle))
	return sb.String()
}

// renderNestedToolBody keeps visible content under the same connector that
// ends at the adjacent terminal summary. Empty content adds no decorative row.
func renderNestedToolBody(content string, width int) string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return ""
	}

	var sb strings.Builder
	for line := range strings.SplitSeq(content, "\n") {
		line = visibleLine(line)
		if strings.TrimSpace(line) == "" {
			sb.WriteString(strings.Repeat(" ", lipgloss.Width(nestedBodyPrefix)) + "\n")
			continue
		}
		sb.WriteString(renderNestedToolBodyLine(line, width))
	}
	return sb.String()
}

// visibleLine drops the progress a tool drew in place and then wrote over —
// git's "Rebasing (1/3)\r…". Left in, the carriage return lands inside the "┊"
// gutter and the terminal restarts the row at column 0, connector and all. A
// trailing one is CRLF, not an overwrite.
func visibleLine(line string) string {
	line = strings.TrimSuffix(line, "\r")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		return line[i+1:]
	}
	return line
}

func renderNestedToolBodyLine(line string, width int) string {
	return renderGutteredLine(nestedBodyPrefix, visibleLine(line), width)
}

// renderGutteredLine soft-wraps one row to the terminal width so every
// continuation row keeps the "┊" gutter. Left to the terminal, a wrapped row
// restarts at column 0 and the connector breaks. Tabs expand to lipgloss's
// tab width up front so the wrap budget matches what Render will draw.
func renderGutteredLine(prefix, line string, width int) string {
	if width <= 0 {
		width = 80
	}
	line = strings.ReplaceAll(line, "\t", "    ")
	wrapWidth := max(1, width-lipgloss.Width(nestedBodyPrefix))

	var sb strings.Builder
	for segment := range strings.SplitSeq(xansi.Wrap(line, wrapWidth, " "), "\n") {
		sb.WriteString(toolResultStyle.Render(prefix+segment) + "\n")
		prefix = nestedBodyPrefix
	}
	return sb.String()
}

func renderNestedToolBodyContinuous(content string, width int) string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return ""
	}

	var sb strings.Builder
	for line := range strings.SplitSeq(content, "\n") {
		sb.WriteString(renderNestedToolBodyLine(line, width))
	}
	return sb.String()
}

func renderNestedToolTrailer(summary string, style lipgloss.Style) string {
	return style.Render(nestedTrailerPrefix+summary) + "\n"
}

func renderNestedFailure(content string, width int) string {
	content = strings.TrimPrefix(content, "Error: ")
	return renderNestedToolBody(content, width) + renderNestedToolTrailer("failed", errorStyle)
}

func renderNestedGenericToolResultInline(data ToolResultData) string {
	if data.IsError {
		return renderNestedFailure(data.Content, data.Width)
	}

	content := strings.TrimSuffix(data.Content, "\n")
	var sb strings.Builder
	if data.Expanded {
		sb.WriteString(renderNestedToolBody(content, data.Width))
	}
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}
	sb.WriteString(renderNestedToolTrailer(formatToolResultSize(toolName, content), toolResultStyle))
	return sb.String()
}

func formatReadResultSummary(content string) string {
	count := 0
	for line := range strings.SplitSeq(content, "\n") {
		prefix, _, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(prefix)); err == nil {
			count++
		}
	}
	if count > 0 {
		return formatLineCountValue(count)
	}
	if strings.HasPrefix(content, "file exists but is empty:") {
		return "empty file"
	}
	if strings.HasPrefix(content, "no lines at offset ") {
		return "no lines"
	}
	if strings.HasPrefix(content, "Binary file detected:") {
		return "binary file"
	}
	if strings.HasPrefix(content, "image file:") {
		return "image file"
	}
	return formatLineCount(content)
}

func renderBashToolResultInline(data ToolResultData) string {
	content := strings.TrimSuffix(data.Content, "\n")
	summary := formatLineCount(content)
	style := toolResultStyle
	if data.IsError {
		content, summary = formatBashFailure(content, data.Details)
		style = errorStyle
	}

	var sb strings.Builder
	showBody := (data.Expanded || data.IsError) && content != ""
	if showBody {
		sb.WriteString(renderNestedToolBodyContinuous(content, data.Width))
	}
	sb.WriteString(renderNestedToolTrailer(summary, style))
	return sb.String()
}

func formatBashFailure(content string, details any) (string, string) {
	if bashDetails, ok := details.(toolresult.BashDetails); ok {
		content = trimBashErrorSuffix(content, bashDetails.Error)
		return content, formatBashFailureSummary(bashDetails.Error, bashDetails.LineCount)
	}

	if output, reason, ok := splitBashFailureContent(content); ok {
		lineCount := 0
		if strings.TrimSuffix(output, "\n") != "" {
			lineCount = strings.Count(strings.TrimSuffix(output, "\n"), "\n") + 1
		}
		return output, formatBashFailureSummary(reason, lineCount)
	}

	return content, "failed · " + formatLineCount(content)
}

func formatBashFailureSummary(reason string, lineCount int) string {
	summary := "failed"
	if reason = formatBashFailureReason(reason); reason != "" {
		summary += " · " + reason
	}
	if lineCount > 0 {
		summary += " · " + formatLineCountValue(lineCount)
	}
	return summary
}

func splitBashFailureContent(content string) (output, reason string, ok bool) {
	if reason, ok := strings.CutPrefix(content, "Error: "); ok {
		return "", reason, true
	}
	const separator = "\nError: "
	index := strings.LastIndex(content, separator)
	if index < 0 {
		return "", "", false
	}
	return content[:index], content[index+len(separator):], true
}

func trimBashErrorSuffix(content, errorMessage string) string {
	if content == "Error: "+errorMessage {
		return ""
	}
	return strings.TrimSuffix(content, "\nError: "+errorMessage)
}

func formatBashFailureReason(reason string) string {
	const timeoutPrefix = "command timed out after "
	if after, ok := strings.CutPrefix(reason, timeoutPrefix); ok {
		duration, _, _ := strings.Cut(after, " — ")
		return "timed out after " + duration
	}
	return reason
}

func renderNestedFileChangeResultInline(data ToolResultData) string {
	width := data.Width
	if width <= 0 {
		width = 80
	}

	var sb strings.Builder
	if data.IsError {
		sb.WriteString(renderFileChangeInputPreview(data.ToolInput, data.Content, width))
		sb.WriteString(renderNestedFailure(data.Content, width))
		return sb.String()
	}

	if details, ok := data.Details.(toolresult.FileChangeDetails); ok {
		block, _ := renderStoredFileDiffIndented(details.UnifiedDiff, width, 0, nestedBodyPrefix)
		sb.WriteString(block)
		if details.TruncatedDiffLines > 0 {
			sb.WriteString(truncatedStyle.Render(fmt.Sprintf(nestedBodyPrefix+"… diff truncated (%d more lines)", details.TruncatedDiffLines)) + "\n")
		}
		sb.WriteString(renderNestedToolTrailer(fileChangeSummary(details), toolResultStyle))
		return sb.String()
	}

	sb.WriteString(renderNestedToolTrailer(extractTrailingParenContent(data.Content, "completed"), toolResultStyle))
	return sb.String()
}

// renderFileChangeInputPreview shows the requested change when the edit did
// not apply. It uses tool input rather than diagnostics so the user can compare
// the intended replacement with the actual-file diagnostic below.
func renderFileChangeInputPreview(input, diagnostic string, width int) string {
	var params struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		OldText   string `json:"oldText"`
		NewText   string `json:"newText"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
			OldText   string `json:"oldText"`
			NewText   string `json:"newText"`
		} `json:"edits"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(input), &params) != nil {
		return ""
	}
	old, new := params.OldString, params.NewString
	if old == "" && new == "" {
		old, new = params.OldText, params.NewText
	}
	if old == "" && new == "" && len(params.Edits) > 0 {
		index := editIndexFromDiagnostic(diagnostic)
		if index < 0 || index >= len(params.Edits) {
			index = 0
		}
		edit := params.Edits[index]
		old, new = edit.OldString, edit.NewString
		if old == "" && new == "" {
			old, new = edit.OldText, edit.NewText
		}
	}
	if new == "" && params.Content != "" {
		new = params.Content
	}
	previewWidth := width - lipgloss.Width(nestedBodyPrefix)
	if previewWidth <= lipgloss.Width("+ ") {
		return ""
	}
	var sb strings.Builder
	for _, preview := range []struct{ marker, text string }{{"-", old}, {"+", new}} {
		if preview.text == "" {
			continue
		}
		for line := range strings.SplitSeq(preview.text, "\n") {
			for segment := range strings.SplitSeq(xansi.Wrap(line, previewWidth-lipgloss.Width(preview.marker)-1, " "), "\n") {
				sb.WriteString(toolResultStyle.Render(nestedBodyPrefix+preview.marker+" "+segment) + "\n")
			}
		}
	}
	return sb.String()
}

func editIndexFromDiagnostic(diagnostic string) int {
	const prefix = "edits["
	start := strings.Index(diagnostic, prefix)
	if start < 0 {
		return -1
	}
	start += len(prefix)
	end := strings.IndexByte(diagnostic[start:], ']')
	if end < 0 {
		return -1
	}
	index, err := strconv.Atoi(diagnostic[start : start+end])
	if err != nil {
		return -1
	}
	return index
}

func extractTrailingParenContent(s, fallback string) string {
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return fallback
	}

	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				candidate := s[i+1 : end]
				if strings.Contains(candidate, " lines") || strings.Contains(candidate, "replacement(s)") {
					return candidate
				}
				return fallback
			}
		}
	}
	return fallback
}

func fileChangeSummary(details toolresult.FileChangeDetails) string {
	return fmt.Sprintf("+%d -%d", details.AddedLines, details.RemovedLines)
}

func renderFileChangeResultInline(data ToolResultData) string {
	if data.IsError {
		// Same "failed" summary + indented reason as the generic error branch;
		// only the redundant "Error: " prefix is dropped first.
		data.Content = strings.TrimPrefix(data.Content, "Error: ")
		return renderGenericToolResultInline(data)
	}
	details, ok := data.Details.(toolresult.FileChangeDetails)
	if !ok {
		return renderGenericToolResultInline(data)
	}

	summary := fileChangeSummary(details)

	var sb strings.Builder
	sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  %s → %s", toolResultIcon(false), data.ToolName, summary)) + "\n")

	width := data.Width
	if width <= 0 {
		width = 80
	}
	// The diff renders in full: scrollback freezes, so anything hidden here
	// would be lost for good. The only bound is the cap applied when the
	// diff was stored, reported below from its structured count.
	block, _ := RenderStoredFileDiff(details.UnifiedDiff, width, 0)
	sb.WriteString(block)
	if details.TruncatedDiffLines > 0 {
		sb.WriteString(truncatedStyle.Render(fmt.Sprintf("     … diff truncated (%d more lines)", details.TruncatedDiffLines)) + "\n")
	}
	return sb.String()
}

func renderGenericToolResultInline(data ToolResultData) string {
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}
	sizeInfo := formatToolResultSize(toolName, data.Content)
	if data.IsError {
		// The reason is shown in full on the expanded lines below (always
		// rendered for errors), so keep the summary a plain "failed" rather than
		// repeating the first content line.
		sizeInfo = "failed"
	}
	icon := toolResultIcon(data.IsError)

	summaryStyle := toolResultStyle
	if data.IsError {
		summaryStyle = errorStyle
	}

	var sb strings.Builder
	sb.WriteString(summaryStyle.Render(fmt.Sprintf("  %s  %s → %s", icon, toolName, sizeInfo)) + "\n")
	if data.Expanded || data.IsError {
		for line := range strings.SplitSeq(data.Content, "\n") {
			if data.IsError {
				line = " " + line
			}
			sb.WriteString(toolResultExpandedStyle.Render(line) + "\n")
		}
	}
	return sb.String()
}

func renderAskUserResultInline(data ToolResultData) string {
	icon := toolResultIcon(data.IsError)

	if data.IsError {
		return toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, data.Content)) + "\n"
	}

	if strings.Contains(data.Content, "User cancelled") {
		return toolResultStyle.Render(fmt.Sprintf("  %s  Cancelled", icon)) + "\n"
	}

	var answers []string
	for line := range strings.SplitSeq(data.Content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "User responses:" {
			continue
		}
		if answers == nil {
			answers = make([]string, 0, 4)
		}
		answers = append(answers, line)
	}

	if len(answers) == 0 {
		return toolResultStyle.Render(fmt.Sprintf("  %s  Answered", icon)) + "\n"
	}

	var sb strings.Builder
	for _, a := range answers {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, a)) + "\n")
	}
	return sb.String()
}

func renderSkillResultInline(data ToolResultData) string {
	icon := toolResultIcon(data.IsError)

	var sb strings.Builder
	if data.IsError {
		summary := toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, data.Content))
		sb.WriteString(summary + "\n")
		return sb.String()
	}

	skillName, scriptCount, refCount := parseSkillResultContent(data.Content)
	resources := make([]string, 0, 2)
	if scriptCount > 0 {
		if scriptCount == 1 {
			resources = append(resources, "1 script")
		} else {
			resources = append(resources, fmt.Sprintf("%d scripts", scriptCount))
		}
	}
	if refCount > 0 {
		if refCount == 1 {
			resources = append(resources, "1 ref")
		} else {
			resources = append(resources, fmt.Sprintf("%d refs", refCount))
		}
	}

	result := fmt.Sprintf("Loaded: %s", skillName)
	if len(resources) > 0 {
		result += fmt.Sprintf(" [%s]", strings.Join(resources, ", "))
	}

	summary := toolResultStyle.Render(fmt.Sprintf("  %s  %s", icon, result))
	sb.WriteString(summary + "\n")

	if data.Expanded {
		for line := range strings.SplitSeq(data.Content, "\n") {
			sb.WriteString(toolResultExpandedStyle.Render(line) + "\n")
		}
	}

	return sb.String()
}

func renderTaskResultInline(data ToolResultData, mdRenderer *MDRenderer) string {
	icon := toolResultIcon(data.IsError)

	var sb strings.Builder
	content := data.Content

	if data.IsError {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  Agent → Error", icon)) + "\n")
		sb.WriteString(toolResultExpandedStyle.Render("    "+content) + "\n")
		return sb.String()
	}

	taskID := extractField(content, "Task ID: ", "")
	isBackground := strings.Contains(content, "started in background")
	if isBackground && taskID != "" {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  → background (Task ID: %s)", icon, taskID)) + "\n")
		return sb.String()
	}

	toolUses := extractIntField(content, "ToolUses: ")
	tokens := extractIntField(content, "Tokens: ")
	duration := extractField(content, "Duration: ", "")
	resultModel := extractField(content, "Model: ", "\n")
	doneStats := buildDoneStats(toolUses, tokens, duration, resultModel)

	if !data.Expanded {
		resultLine := fmt.Sprintf("  %s  Done", icon)
		if doneStats != "" {
			resultLine += " (" + doneStats + ")"
		}
		sb.WriteString(toolResultStyle.Render(resultLine))
		if data.Interactive {
			sb.WriteString(ThinkingStyle.Render("  (ctrl+o to expand)"))
		}
		sb.WriteString("\n")
		return sb.String()
	}

	if data.ToolInput != "" {
		w := 80
		if mdRenderer != nil {
			w = mdRenderer.width
		}
		sb.WriteString(formatAgentDefinition(parseAgentInput(data.ToolInput), w))
	}

	body := ""
	if _, rest, found := strings.Cut(content, "\n\n"); found {
		body = rest
	}
	processCount := extractIntField(content, "Process: ")
	process, response := splitByProcessCount(body, processCount)

	if process != "" {
		for line := range strings.SplitSeq(process, "\n") {
			sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  %s", line)) + "\n")
		}
	}

	if response != "" {
		sb.WriteString(agentLabelStyle.Render("  ⎿  Response:") + "\n")
		rendered := response
		if mdRenderer != nil {
			if md, err := mdRenderer.agentBodyRenderer().Render(response); err == nil {
				rendered = strings.TrimSpace(md)
			}
		}
		for line := range strings.SplitSeq(rendered, "\n") {
			sb.WriteString(toolResultExpandedStyle.Render(agentContentIndent+line) + "\n")
		}
	}

	resultLine := "  ⎿  Done"
	if doneStats != "" {
		resultLine += " (" + doneStats + ")"
	}
	sb.WriteString(toolResultStyle.Render(resultLine) + "\n")
	return sb.String()
}

func splitByProcessCount(body string, processCount int) (process, response string) {
	if body == "" {
		return "", ""
	}
	if processCount <= 0 {
		return "", strings.TrimSpace(body)
	}

	lines := strings.SplitN(body, "\n", processCount+1)
	if len(lines) <= processCount {
		return strings.TrimSpace(strings.Join(lines, "\n")), ""
	}
	processLines := lines[:processCount]
	rest := lines[processCount]
	return strings.TrimSpace(strings.Join(processLines, "\n")), strings.TrimSpace(rest)
}

func formatAgentDefinition(agent agentInput, width int) string {
	if !agent.Valid {
		return ""
	}

	var sb strings.Builder
	meta := make([]string, 0, 2)
	if agent.Mode != "" {
		meta = append(meta, fmt.Sprintf("mode=%s", agent.Mode))
	}
	if agent.Background {
		meta = append(meta, "background")
	}
	if len(meta) > 0 {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  ⎿  [%s]", strings.Join(meta, ", "))) + "\n")
	}

	if agent.Prompt != "" {
		sb.WriteString(agentLabelStyle.Render("  ⎿  Prompt:") + "\n")
		wrapWidth := width - lipgloss.Width(agentContentIndent)
		for line := range strings.SplitSeq(agent.Prompt, "\n") {
			for _, wrapped := range wrapLine(line, wrapWidth) {
				sb.WriteString(toolResultExpandedStyle.Render(agentContentIndent+wrapped) + "\n")
			}
		}
	}

	return sb.String()
}

func wrapLine(line string, width int) []string {
	if width <= 0 || lipgloss.Width(line) <= width {
		return []string{line}
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	curWidth := lipgloss.Width(current)
	for _, w := range words[1:] {
		ww := lipgloss.Width(w)
		if curWidth+1+ww > width {
			lines = append(lines, current)
			current = w
			curWidth = ww
		} else {
			current += " " + w
			curWidth += 1 + ww
		}
	}
	return append(lines, current)
}

func buildDoneStats(toolUses, tokens int, duration, model string) string {
	stats := make([]string, 0, 4)
	if toolUses == 1 {
		stats = append(stats, "1 tool use")
	} else if toolUses > 1 {
		stats = append(stats, fmt.Sprintf("%d tool uses", toolUses))
	}
	if tokens > 0 {
		stats = append(stats, kit.FormatTokenCount(tokens)+" tokens")
	}
	if duration != "" {
		stats = append(stats, duration)
	}
	if model != "" {
		stats = append(stats, model)
	}
	return strings.Join(stats, " · ")
}

func parseSkillResultContent(content string) (skillName string, scriptCount, refCount int) {
	skillName = "skill"
	if idx := strings.Index(content, `<skill-invocation name="`); idx != -1 {
		start := idx + len(`<skill-invocation name="`)
		if end := strings.Index(content[start:], `"`); end != -1 {
			skillName = content[start : start+end]
		}
	}

	if idx := strings.Index(content, "Available scripts"); idx != -1 {
		section := content[idx:]
		lines := strings.Split(section, "\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(line, "  - ") {
				scriptCount++
			} else if line == "" || !strings.HasPrefix(line, " ") {
				break
			}
		}
	}

	if idx := strings.Index(content, "Reference files"); idx != -1 {
		section := content[idx:]
		lines := strings.Split(section, "\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if strings.HasPrefix(line, "  - ") {
				refCount++
			} else if line == "" || !strings.HasPrefix(line, " ") {
				break
			}
		}
	}

	return skillName, scriptCount, refCount
}

func extractField(content, prefix, defaultVal string) string {
	idx := strings.Index(content, prefix)
	if idx == -1 {
		return defaultVal
	}
	start := idx + len(prefix)
	end := strings.Index(content[start:], "\n")
	if end == -1 {
		return content[start:]
	}
	return content[start : start+end]
}

func extractIntField(content, prefix string) int {
	val := extractField(content, prefix, "")
	if val == "" {
		return 0
	}
	end := 0
	for end < len(val) && val[end] >= '0' && val[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(val[:end])
	return n
}

func formatAgentLabel(agent agentInput) string {
	if !agent.Valid {
		return "Agent"
	}

	desc := ""
	if agent.Description != "" {
		desc = conciseAgentDescription(agent.Description)
	} else if agent.Prompt != "" {
		desc = conciseAgentDescription(agent.Prompt)
	}

	if desc != "" {
		return fmt.Sprintf("Agent - %s: %s", agent.Name, desc)
	}
	return fmt.Sprintf("Agent - %s", agent.Name)
}

func conciseAgentDescription(desc string) string {
	words := strings.Fields(desc)
	if len(words) > 10 {
		return strings.Join(words[:10], " ") + "..."
	}
	return kit.TruncateText(desc, 60)
}

func displayAgentName(agentName, mode string) string {
	if strings.TrimSpace(agentName) == "" {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "explore":
			return "Explorer"
		case "edit":
			return "Editor"
		}
		return "General"
	}
	return shortAgentName(agentName)
}

type agentInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode"`
	Background  bool   `json:"run_in_background"`
	Valid       bool   `json:"-"`
}

func parseAgentInput(input string) agentInput {
	var agent agentInput
	if err := json.Unmarshal([]byte(input), &agent); err != nil {
		return agentInput{}
	}
	agent.Valid = true
	if agent.Name == "" {
		agent.Name = displayAgentName("", agent.Mode)
	}
	return agent
}

func shortAgentName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	kept := make([]string, 0, 2)
	for _, word := range words {
		word = strings.ToLower(strings.TrimSpace(word))
		if word == "" || word == "current" || word == "change" || word == "changes" {
			continue
		}
		kept = append(kept, word)
		if len(kept) == 2 {
			break
		}
	}
	if len(kept) == 0 {
		return "Agent"
	}
	for i, word := range kept {
		kept[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(kept, " ")
}

// taskGetIsList reports whether a TaskGet call is a list-all invocation (no
// taskId) rather than a single-task lookup.
func taskGetIsList(input string) bool {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return false
	}
	id, _ := params["taskId"].(string)
	return id == ""
}

func extractTaskGetDisplay(input string, ownerMap map[string]string) string {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	id, _ := params["taskId"].(string)
	if owner, ok := ownerMap[id]; ok && owner != "" {
		return owner
	}
	return id
}

func extractToolArgs(input string) string {
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}

	if fp, ok := params["file_path"].(string); ok {
		return fp
	}
	if c, ok := params["command"].(string); ok {
		return c
	}
	if p, ok := params["pattern"].(string); ok {
		return p
	}
	if p, ok := params["path"].(string); ok {
		return p
	}
	if u, ok := params["url"].(string); ok {
		return u
	}
	if s, ok := params["skill"].(string); ok {
		return s
	}
	if qs, ok := params["questions"].([]any); ok {
		count := len(qs)
		if count == 1 {
			return "1 question"
		}
		return fmt.Sprintf("%d questions", count)
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := params[k].(string); ok {
			return s
		}
	}
	return ""
}

func formatToolResultSize(toolName, content string) string {
	switch toolName {
	case "WebFetch":
		return toolresult.FormatSize(int64(len(content)))
	case "Write", "Edit":
		return extractParenContent(content, "completed")
	default:
		return formatLineCount(content)
	}
}

func extractParenContent(s, fallback string) string {
	start := strings.IndexByte(s, '(')
	if start == -1 {
		return fallback
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[start+1 : i]
			}
		}
	}
	return fallback
}

func formatLineCount(content string) string {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return "no output"
	}
	return formatLineCountValue(strings.Count(trimmed, "\n") + 1)
}

func formatLineCountValue(lineCount int) string {
	if lineCount == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", lineCount)
}

// renderBashDescription dims the description in parentheses after the Bash
// label, shortening the text — never the closing paren — to fit width.
func renderBashDescription(description string, width int) string {
	inner := width - lipgloss.Width(" ()")
	if description == "" || inner <= 0 {
		return ""
	}
	return toolResultStyle.Render(" (" + xansi.Truncate(description, inner, "...") + ")")
}

func renderToolLine(label string, width int) string {
	return renderToolLineWithIcon(label, width, "●")
}

func renderToolLineWithIcon(label string, width int, iconText string) string {
	icon := toolCallStyle.Width(2).Render(iconText)
	return lipgloss.JoinHorizontal(lipgloss.Top, icon, toolCallStyle.Render(truncateToolLabel(label, width)))
}

// renderShellToolCall renders a shell tool call without ever truncating its
// command. A single-line command stays in Bash(command) form only when that
// complete label fits; its optional description may be shortened to keep the
// row within the terminal. Commands that contain a newline or do not fit become
// a full command block below the Bash header and soft-wrap without ellipses.
func renderShellToolCall(toolName, input string, width int, icon, detail string) string {
	command, description := extractBashCommand(input)
	description = strings.Join(strings.Fields(description), " ")
	labelWidth := max(3, bashPreviewLabelWidth(width)-lipgloss.Width(detail))

	if strings.TrimSpace(command) != "" && !strings.Contains(command, "\n") {
		commandLabel := fmt.Sprintf("%s(%s)", toolName, command)
		if lipgloss.Width(commandLabel) <= labelWidth {
			label := toolCallStyle.Render(commandLabel)
			label += renderBashDescription(description, labelWidth-lipgloss.Width(commandLabel))
			iconCell := toolCallStyle.Width(2).Render(icon)
			return lipgloss.JoinHorizontal(lipgloss.Top, iconCell, label) + detail + "\n"
		}
	}
	if strings.TrimSpace(command) == "" {
		command = "(no command)"
	}

	var sb strings.Builder

	// Header line: ● Bash (description) · running detail. Only the description
	// may be shortened; the command is rendered separately below.
	header := toolCallStyle.Render(toolName) + renderBashDescription(description, labelWidth-lipgloss.Width(toolName))
	iconCell := toolCallStyle.Width(2).Render(icon)
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, iconCell, header) + detail + "\n")

	// Soft-wrap every command line to the terminal width. The first row carries
	// the shell prompt; every later row uses the connector so the command and its
	// result form one continuous block. No command text is replaced by ellipses.
	seenCommand := false
	for commandLine := range strings.SplitSeq(command, "\n") {
		// Preserve intentional blank lines after the command starts while keeping
		// the connector continuous. Leading blank lines are still skipped so the
		// first visible command retains "$".
		if strings.TrimSpace(commandLine) == "" {
			if seenCommand {
				sb.WriteString(renderNestedToolBodyLine("", width))
			}
			continue
		}

		prefix := bashPrompt
		if seenCommand {
			prefix = nestedBodyPrefix
		}
		seenCommand = true
		sb.WriteString(renderGutteredLine(prefix, commandLine, width))
	}
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
	return params.Command, params.Description
}

func renderAgentToolLine(label string, width int, iconText string, color string) string {
	style := agentStyle(color)
	icon := style.Width(2).Render(iconText)
	return lipgloss.JoinHorizontal(lipgloss.Top, icon, style.Render(truncateToolLabel(label, width)))
}

func agentStyle(color string) lipgloss.Style {
	return toolCallStyle.Foreground(agentColor(color))
}

func agentColor(color string) kit.AdaptiveColor {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "blue":
		return kit.CurrentTheme.Primary
	case "yellow":
		return kit.CurrentTheme.Warning
	case "gray", "grey":
		return kit.CurrentTheme.Muted
	case "accent":
		return kit.CurrentTheme.Accent
	case "ai":
		return kit.CurrentTheme.AI
	case "green", "":
		return kit.CurrentTheme.Success
	default:
		if strings.HasPrefix(color, "#") {
			return kit.AdaptiveColor{Dark: color, Light: color}
		}
		return kit.CurrentTheme.Success
	}
}

// configuredAgentColor returns the color set for this agent's name in its
// configuration (a name like "blue" or a "#rrggbb" hex), or "" when none is
// set. It is later resolved to a theme color by agentColor.
func configuredAgentColor(agent agentInput, colors map[string]string) string {
	if len(colors) == 0 {
		return ""
	}
	return colors[strings.ToLower(agent.Name)]
}

// agentBlinkTicks is the number of spinner ticks per ● / ○ swap.
// One spinner tick is ~360ms (see newFrameClock in model.go), so 2 ticks
// gives the familiar ~720ms blink.
const agentBlinkTicks = 2

func agentIcon(tick int) string {
	if (tick/agentBlinkTicks)%2 == 0 {
		return "●"
	}
	return "○"
}

func truncateToolLabel(label string, width int) string {
	maxWidth := maxToolLabelWidth(width)
	if lipgloss.Width(label) <= maxWidth {
		return label
	}
	return kit.TruncateText(label, maxWidth)
}

func bashPreviewLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	return max(20, width-lipgloss.Width("● "))
}

func maxToolLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	maxWidth := max(width*80/100, 50)
	labelWidth := maxWidth - lipgloss.Width("● ")
	if labelWidth < 20 {
		return 20
	}
	return labelWidth
}
