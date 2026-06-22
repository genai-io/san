package conv

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

var (
	headerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(kit.CurrentTheme.Border).
			Padding(0, 1)

	headerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(kit.CurrentTheme.Primary)

	headerSubtitleStyle = lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Text)

	headerMetaStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted)

	lineNumberStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Width(5).
			Align(lipgloss.Right)

	matchStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Warning).
			Bold(true)

	filePathStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Primary)

	truncatedStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Muted).
			Italic(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(kit.CurrentTheme.Error)
)

// RenderToolResult renders a complete tool result with header and content.
func RenderToolResult(result toolresult.ToolResult, width int) string {
	if !result.Success {
		return renderErrorHeader(result.Metadata.Title, result.Error, width)
	}

	var sb strings.Builder

	sb.WriteString(renderHeader(result.Metadata, width))
	sb.WriteString("\n")

	switch result.Metadata.Title {
	case "Read":
		if len(result.Lines) > 0 {
			sb.WriteString(renderLines(result.Lines))
		} else if result.Output != "" {
			sb.WriteString(result.Output)
		}
	case "Glob":
		if len(result.Files) > 0 {
			sb.WriteString(renderFileList(result.Files, 20))
		} else if result.Output != "" {
			sb.WriteString(result.Output)
		}
	case "Grep":
		if len(result.Lines) > 0 {
			sb.WriteString(renderGrepResults(result.Lines, 30))
		} else if result.Output != "" {
			sb.WriteString(result.Output)
		}
	case "WebFetch":
		if result.Output != "" {
			lines := strings.Split(result.Output, "\n")
			for _, line := range lines {
				sb.WriteString("  ")
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
	default:
		if result.Output != "" {
			sb.WriteString(result.Output)
		}
	}

	return sb.String()
}

// RenderToolResultInline renders a tool result inline (without leading newline).
func RenderToolResultInline(data ToolResultData, mdRenderer *MDRenderer) string {
	toolName := data.ToolName
	if toolName == "" {
		toolName = "Tool"
	}

	switch toolName {
	case tool.ToolSkill:
		return renderSkillResultInline(data)
	case tool.ToolAgent, tool.ToolSendMessage:
		return renderTaskResultInline(data, mdRenderer)
	case tool.ToolTaskOutput:
		return renderTaskOutputResultInline(data)
	case tool.ToolAskUserQuestion:
		return renderAskUserResultInline(data)
	}

	sizeInfo := formatToolResultSize(toolName, data.Content)
	icon := toolResultIcon(data.IsError)

	// Errors break out of the dim plumbing tone into the error color so a
	// failed call is visible at a glance; successes stay muted.
	summaryStyle := toolResultStyle
	if data.IsError {
		summaryStyle = errorStyle
	}

	var sb strings.Builder
	summary := summaryStyle.Render(fmt.Sprintf("  %s  %s → %s", icon, toolName, sizeInfo))
	sb.WriteString(summary + "\n")

	if data.Expanded || data.IsError {
		for line := range strings.SplitSeq(data.Content, "\n") {
			sb.WriteString(toolResultExpandedStyle.Render(line) + "\n")
		}
	}

	return sb.String()
}

func renderHeader(meta toolresult.ResultMetadata, width int) string {
	title := headerTitleStyle.Render(meta.Title)
	subtitle := fmt.Sprintf("%s %s", meta.Icon, headerSubtitleStyle.Render(meta.Subtitle))

	metaParts := make([]string, 0, 6)
	if meta.Size > 0 {
		metaParts = append(metaParts, toolresult.FormatSize(meta.Size))
	}
	if meta.LineCount > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d lines", meta.LineCount))
	}
	if meta.ItemCount > 0 {
		switch meta.Title {
		case "Glob":
			metaParts = append(metaParts, fmt.Sprintf("%d files", meta.ItemCount))
		case "Grep":
			metaParts = append(metaParts, fmt.Sprintf("%d matches", meta.ItemCount))
		default:
			metaParts = append(metaParts, fmt.Sprintf("%d items", meta.ItemCount))
		}
	}
	if meta.StatusCode > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d OK", meta.StatusCode))
	}
	if meta.Duration > 0 {
		metaParts = append(metaParts, toolresult.FormatDuration(meta.Duration))
	}
	if meta.Truncated {
		metaParts = append(metaParts, truncatedStyle.Render("(truncated)"))
	}
	metaLine := headerMetaStyle.Render(strings.Join(metaParts, " · "))

	content := fmt.Sprintf("%s\n%s\n%s", title, subtitle, metaLine)
	box := headerStyle.Width(capBoxWidth(width) - 4).Render(content)
	return box
}

func renderErrorHeader(toolName, errorMsg string, width int) string {
	title := headerTitleStyle.Render(toolName)
	errorLine := fmt.Sprintf("%s %s", toolresult.IconError, errorStyle.Render("Error"))
	msgLine := errorStyle.Render(errorMsg)

	content := fmt.Sprintf("%s\n%s\n%s", title, errorLine, msgLine)

	errorBoxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(kit.CurrentTheme.Error).
		Padding(0, 1)

	box := errorBoxStyle.Width(capBoxWidth(width) - 4).Render(content)
	return box
}

func capBoxWidth(width int) int {
	if width <= 0 {
		return 50
	}
	maxWidth := width * 80 / 100
	if maxWidth < 50 {
		return 50
	}
	return maxWidth
}

func renderLines(lines []toolresult.ContentLine) string {
	if len(lines) == 0 {
		return ""
	}

	var sb strings.Builder

	maxLineNo := 0
	for _, line := range lines {
		if line.LineNo > maxLineNo {
			maxLineNo = line.LineNo
		}
	}
	lineNoWidth := len(fmt.Sprintf("%d", maxLineNo))
	if lineNoWidth < 4 {
		lineNoWidth = 4
	}

	for _, line := range lines {
		switch line.Type {
		case toolresult.LineTruncated:
			sb.WriteString(truncatedStyle.Render(line.Text))
			sb.WriteString("\n")
		default:
			if line.LineNo > 0 {
				lineNoStr := fmt.Sprintf("%*d", lineNoWidth, line.LineNo)
				sb.WriteString(lineNumberStyle.Render(lineNoStr))
			} else {
				sb.WriteString(strings.Repeat(" ", lineNoWidth))
			}
			sb.WriteString(lineNumberStyle.Render("│"))

			var content string
			switch line.Type {
			case toolresult.LineMatch:
				content = matchStyle.Render(line.Text)
			case toolresult.LineHeader:
				content = filePathStyle.Render(line.Text)
			default:
				content = line.Text
			}
			sb.WriteString(content)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func renderFileList(files []string, maxShow int) string {
	if len(files) == 0 {
		return truncatedStyle.Render("  (no files found)\n")
	}

	var sb strings.Builder
	showCount := len(files)
	truncated := false
	if maxShow > 0 && showCount > maxShow {
		showCount = maxShow
		truncated = true
	}

	for i := 0; i < showCount; i++ {
		sb.WriteString("  ")
		sb.WriteString(filePathStyle.Render(files[i]))
		sb.WriteString("\n")
	}

	if truncated {
		remaining := len(files) - maxShow
		sb.WriteString(truncatedStyle.Render(fmt.Sprintf("  ... and %d more files\n", remaining)))
	}

	return sb.String()
}

func renderGrepResults(lines []toolresult.ContentLine, maxShow int) string {
	if len(lines) == 0 {
		return truncatedStyle.Render("  (no matches found)\n")
	}

	var sb strings.Builder
	showCount := len(lines)
	truncated := false
	if maxShow > 0 && showCount > maxShow {
		showCount = maxShow
		truncated = true
	}

	for i := 0; i < showCount; i++ {
		line := lines[i]
		sb.WriteString("  ")
		if line.File != "" {
			sb.WriteString(filePathStyle.Render(line.File))
			sb.WriteString(":")
		}
		if line.LineNo > 0 {
			sb.WriteString(lineNumberStyle.Render(fmt.Sprintf("%d", line.LineNo)))
			sb.WriteString(": ")
		}
		sb.WriteString(line.Text)
		sb.WriteString("\n")
	}

	if truncated {
		remaining := len(lines) - maxShow
		sb.WriteString(truncatedStyle.Render(fmt.Sprintf("  ... and %d more matches\n", remaining)))
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
		sb.WriteString(formatAgentDefinition(data.ToolInput, w))
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
			narrowRenderer := NewMDRenderer(mdRenderer.width - len(agentContentIndent))
			if md, err := narrowRenderer.Render(response); err == nil {
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

func renderTaskOutputResultInline(data ToolResultData) string {
	icon := toolResultIcon(data.IsError)

	var sb strings.Builder
	content := data.Content

	if data.IsError {
		sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  TaskOutput → Error", icon)) + "\n")
		if content != "" {
			sb.WriteString(toolResultExpandedStyle.Render("    "+content) + "\n")
		}
		return sb.String()
	}

	agentName := extractField(content, "Agent: ", "")
	status := extractField(content, "Status: ", "")
	steps := extractIntField(content, "Steps: ")

	var info []string
	if agentName != "" {
		info = make([]string, 0, 3)
		info = append(info, agentName)
	}
	if status != "" {
		info = append(info, status)
	}
	if steps > 0 {
		info = append(info, fmt.Sprintf("%d steps", steps))
	}

	summaryText := "completed"
	if len(info) > 0 {
		summaryText = strings.Join(info, ", ")
	}

	sb.WriteString(toolResultStyle.Render(fmt.Sprintf("  %s  TaskOutput → %s", icon, summaryText)) + "\n")

	if _, outputContent, found := strings.Cut(content, "Output:\n"); found {
		outputLines := strings.Split(outputContent, "\n")
		maxLines := 10
		if data.Expanded {
			maxLines = len(outputLines)
		}

		for i, line := range outputLines {
			if i >= maxLines {
				sb.WriteString(toolResultExpandedStyle.Render("    ...") + "\n")
				break
			}
			sb.WriteString(toolResultExpandedStyle.Render("    "+stripMarkdownHeading(line)) + "\n")
		}
	}

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

func formatAgentDefinition(input string, width int) string {
	agent := parseAgentInput(input)
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

func formatAgentLabel(input string) string {
	agent := parseAgentInput(input)
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
	if len(desc) > 60 {
		return desc[:57] + "..."
	}
	return desc
}

func displayAgentName(agentType, mode string) string {
	if isGenericAgentName(agentType) {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "explore":
			return "Explorer"
		case "edit":
			return "Editor"
		}
		switch strings.ToLower(strings.TrimSpace(agentType)) {
		case "explore", "explorer":
			return "Explorer"
		case "edit", "editor":
			return "Editor"
		default:
			return "General"
		}
	}
	return shortAgentName(agentType)
}

func isGenericAgentName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "agent", "general", "general-purpose", "explore", "explorer", "edit", "editor":
		return true
	default:
		return false
	}
}

type agentInput struct {
	Type        string `json:"subagent_type"`
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
	if agent.Type == "" {
		agent.Type = "general-purpose"
	}
	if agent.Name == "" {
		agent.Name = displayAgentName(agent.Type, agent.Mode)
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
	start := strings.Index(s, "(")
	if start == -1 {
		return fallback
	}
	end := strings.Index(s[start:], ")")
	if end == -1 {
		return fallback
	}
	return s[start+1 : start+end]
}

func formatLineCount(content string) string {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return "no output"
	}
	lineCount := strings.Count(trimmed, "\n") + 1
	return fmt.Sprintf("%d lines", lineCount)
}

func renderToolLine(label string, width int) string {
	return renderToolLineWithIcon(label, width, "●")
}

func renderToolLineWithIcon(label string, width int, iconText string) string {
	icon := toolCallStyle.Width(2).Render(iconText)
	return lipgloss.JoinHorizontal(lipgloss.Top, icon, toolCallStyle.Render(truncateToolLabel(label, width)))
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

func agentColorForInput(input string, colors map[string]string) string {
	if len(colors) == 0 {
		return ""
	}
	return colors[strings.ToLower(parseAgentInput(input).Type)]
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

func maxToolLabelWidth(width int) int {
	if width <= 0 {
		return 80
	}
	maxWidth := width * 80 / 100
	if maxWidth < 50 {
		maxWidth = 50
	}
	labelWidth := maxWidth - lipgloss.Width("● ")
	if labelWidth < 20 {
		return 20
	}
	return labelWidth
}
