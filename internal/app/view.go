// Bubble Tea View: composes the terminal UI from active content, input area, and status bar.
package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/todo"
)

var ghostTextStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)

// View dispatches to one of four layouts, top-down:
//
//  1. Loading splash (env not ready yet)
//  2. Active fullscreen overlay (slash-command picker / etc.)
//  3. Active docked modal (Question / Approval) — wrapped between separators
//  4. Normal mode — chat section + status + input strip
//
// The active overlay (cases 2 & 3) comes from activeOverlay — the same
// source the key router uses — so the panel that owns the keyboard is always
// the one drawn on screen.
func (m *model) View() tea.View {
	return tea.NewView(m.viewString())
}

// viewString renders the UI to a styled string; View wraps it in a tea.View.
func (m *model) viewString() string {
	if !m.env.Ready {
		return "\n  " + brandMark() + ghostTextStyle.Render("  ·  Loading…")
	}

	ov, hasOverlay := m.activeOverlay()
	if hasOverlay && !isDockedModal(ov) {
		return ov.Render() // fullscreen slash-command picker
	}

	separator := conv.SeparatorStyle.Render(strings.Repeat("─", m.env.Width))
	trackerView := m.renderTrackerList()

	if hasOverlay { // docked modal (Question / Approval)
		trackerPrefix := ""
		if trackerView != "" {
			trackerPrefix = "\n" + strings.TrimSuffix(trackerView, "\n") + "\n"
		}
		return separatorWrapped(trackerPrefix, separator, ov.Render())
	}
	return m.renderNormalView(separator, trackerView)
}

// isDockedModal reports whether the active overlay docks above the input area
// — rendered between separators with the task tracker still visible — rather
// than taking over the full screen like the slash-command pickers do. Only
// the Question and Approval modals dock.
func isDockedModal(ov overlayPanel) bool {
	switch ov.(type) {
	case *conv.QuestionPrompt, *input.ApprovalModel:
		return true
	}
	return false
}

// renderNormalView composes the standard layout: chat scrollback area,
// turn-usage summary, queue preview, textarea + suggestions, and the
// bottom status line.
//
// Only the active (uncommitted) tail is rendered here; finished messages are
// already in the terminal's native scrollback (committed via tea.Println, see
// model_scrollback.go). The chat section is height-limited so the View()
// output never exceeds the terminal height: when the live tail is taller than
// the space above the input area, its last lines (the latest content) are
// shown and earlier lines scroll off — the full message lands in native
// scrollback at turn end, which the terminal scrolls back through natively.
func (m *model) renderNormalView(separator, trackerView string) string {
	// Render the footer first so we can measure how many lines it consumes
	// and cap the chat section to the remaining terminal height.
	footer := m.renderFooter(separator)
	bottomLines := strings.Count(footer, "\n")

	maxContentHeight := 0
	// Only truncate when there's room for at least one line of content.
	if m.env.Height > bottomLines {
		maxContentHeight = m.env.Height - bottomLines
	}

	activeContent := conv.RenderActiveContent(m.messageRenderParams())
	chatSection := m.renderChatSection(activeContent, trackerView)

	return tailLines(chatSection, maxContentHeight) + footer
}

// tailLines returns the last maxLines newline-delimited lines of s, keeping the
// latest content visible when the live tail is taller than the available
// height. s is returned unchanged when maxLines <= 0 or it already fits.
func tailLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}

// renderFooter renders everything below the chat section (turn usage,
// separators, queue preview, input area, suggestions, status line) into a
// single string so its line count can be measured.
func (m *model) renderFooter(separator string) string {
	var b strings.Builder
	if turnUsage := conv.RenderTurnUsageSummary(m.env.TurnInputTokens, m.env.TurnOutputTokens, m.env.Width); turnUsage != "" {
		b.WriteString("\n")
		b.WriteString(turnUsage)
	}
	b.WriteString("\n")
	b.WriteString(separator)
	if queuePreview := m.renderQueuePreview(); queuePreview != "" {
		b.WriteString("\n")
		b.WriteString(queuePreview)
	}
	b.WriteString("\n")
	b.WriteString(m.renderInputView())
	if suggestions := m.userInput.Suggestions.Render(m.env.Width); suggestions != "" {
		b.WriteString("\n")
		b.WriteString(suggestions)
	}
	b.WriteString("\n")
	b.WriteString(separator)
	b.WriteString("\n")
	if statusLine := m.renderModeStatus(); statusLine != "" {
		b.WriteString(statusLine)
	} else {
		b.WriteString(" ")
	}
	return b.String()
}

func separatorWrapped(trackerPrefix, separator, content string) string {
	return trackerPrefix + separator + "\n" + content
}

func (m model) renderInputView() string {
	prompt := conv.InputPromptStyle.Render("❭ ")
	if m.userInput.PromptSuggestion.Text != "" && m.userInput.Textarea.Value() == "" &&
		!m.conv.Stream.Active && !m.userInput.Suggestions.IsVisible() {
		return prompt + ghostTextStyle.Render(m.userInput.PromptSuggestion.Text)
	}
	return prompt + m.userInput.RenderTextarea()
}

// renderChatSection assembles the active chat content (uncommitted messages,
// tracker, transient spinners) into a single string. Height-limiting is
// applied by the caller (tailLines).
func (m model) renderChatSection(activeContent, trackerView string) string {
	var parts []string

	if activeContent != "" {
		parts = append(parts, activeContent)
	}

	if trackerView != "" {
		// Leading "\n" forces a blank line between the assistant content
		// (often flushed to scrollback via tea.Println) and the tracker
		// block that anchors the bottom of the active view.
		parts = append(parts, "\n"+strings.TrimSuffix(trackerView, "\n"))
	}

	if m.userInput.Provider.FetchingLimits {
		spinnerView := conv.ThinkingStyle.Render(m.conv.Spinner.View() + " Fetching token limits...")
		if len(parts) > 0 {
			spinnerView = "\n" + spinnerView
		}
		parts = append(parts, spinnerView)
	}

	if compactView := conv.RenderCompactStatus(m.env.Width, m.conv.Spinner.View(), m.conv.Compact); compactView != "" {
		parts = append(parts, compactView)
	}

	if live := m.renderSelfLearnLive(); live != "" {
		// Surrounded by blank rows so the inline indicator reads as
		// its own block — clearly separated from active content above
		// (so it doesn't squish against an assistant turn) and the
		// prompt below (the breathing room is what makes the row feel
		// "live" rather than nailed to the input bar).
		parts = append(parts, "", live, "")
	}

	return strings.Join(parts, "\n")
}

// renderSelfLearnLive returns the L1 indicator as an inline live row
// during reviewing / failed phases. The done phase is suppressed here —
// the recap card is published into the conversation flow instead and
// would otherwise duplicate the "✓ <summary>" line. Idle ⇒ "".
func (m model) renderSelfLearnLive() string {
	if m.services.SelfLearn.Indicator == nil {
		return ""
	}
	snap := m.services.SelfLearn.Indicator.Snapshot()
	switch snap.Phase {
	case selflearnReviewing, selflearnFailed:
		return selflearnLiveStyle.Render(snap.Render())
	}
	return ""
}

func (m model) renderTrackerList() string {
	if !m.conv.ShowTasks {
		return ""
	}
	tasks := m.services.Tracker.List()
	return conv.RenderTrackerList(conv.TrackerListParams{
		Tasks:        tasks,
		AllDone:      m.services.Tracker.AllDone(),
		StreamActive: m.conv.Stream.Active,
		Width:        m.env.Width,
		SpinnerView:  m.conv.Spinner.View(),
		Blockers:     m.services.Tracker.OpenBlockers,
		Blink:        m.conv.Spinner.Frame(),
	})
}

func (m model) renderModeStatus() string {
	modelName := m.env.GetModelDisplayName()
	thinkingEffort := m.env.EffectiveThinkingEffort()
	showThinking := true
	if m.env.CurrentModel != nil && m.env.CurrentModel.Provider == llm.OpenAI && thinkingEffort != "" {
		modelName += " (" + thinkingEffort + ")"
		showThinking = false
	}
	if status := m.services.Hook.CurrentStatusMessage(); status != "" {
		modelName = status
	}
	return conv.RenderModeStatus(conv.OperationModeParams{
		Mode:             m.env.OperationMode,
		InputTokens:      m.env.InputTokens,
		InputLimit:       kit.GetEffectiveInputLimit(m.services.LLM.Store(), m.env.CurrentModel),
		ModelName:        modelName,
		StatusMessage:    m.userInput.Provider.StatusMessage,
		ConversationCost: m.env.ConversationCost,
		Compressions:     m.env.Compressions,
		ShowContextBar:   m.env.ShowContextBar,
		Width:            m.env.Width,
		ThinkingEffort:   thinkingEffort,
		ShowThinking:     showThinking,
		QueueCount:       m.userInput.Queue.Len(),
	})
}

func (m model) renderQueuePreview() string {
	rawItems := m.userInput.Queue.Items()
	if len(rawItems) == 0 {
		return ""
	}
	previews := make([]conv.QueuePreviewItem, len(rawItems))
	for i, item := range rawItems {
		previews[i] = conv.QueuePreviewItem{
			Content:   item.Content,
			HasImages: len(item.Images) > 0,
		}
	}

	return strings.TrimSuffix(conv.RenderQueuePreview(previews, m.userInput.Queue.SelectIdx, m.env.Width), "\n")
}

func (m model) messageRenderParams() conv.RenderContext {
	return conv.RenderContext{
		// Conversation state
		Messages:       m.conv.Messages,
		CommittedCount: m.conv.CommittedCount,
		InlinedResults: conv.PrecomputeInlinedResults(m.conv.Messages),

		// Streaming + tool execution
		StreamActive: m.conv.Stream.Active,
		BuildingTool: m.conv.Stream.BuildingTool,
		PendingCalls: m.conv.Tool.PendingCalls,
		CurrentIdx:   m.conv.Tool.CurrentIdx,

		// Renderer env
		Width:      m.env.Width,
		MDRenderer: m.conv.MDRenderer,

		// Per-tick UI state
		SpinnerView:  m.conv.Spinner.View(),
		Blink:        m.conv.Spinner.Frame(),
		ModelName:    m.env.GetModelDisplayName(),
		InputTokens:  m.env.InputTokens,
		OutputTokens: m.env.OutputTokens,

		// Decorations
		AgentColors:  m.agentColors(),
		TaskProgress: m.conv.TaskProgress,
		TaskOwnerMap: buildTaskOwnerMap(m.services.Tracker.List()),

		// Modal interlock
		InteractivePromptActive: m.conv.Modal.Question != nil && m.conv.Modal.Question.IsActive(),
	}
}

func (m model) agentColors() map[string]string {
	return buildAgentColors(m.services.Subagent.ListConfigs())
}

func buildAgentColors(configs []*subagent.AgentConfig) map[string]string {
	if len(configs) == 0 {
		return nil
	}
	colors := make(map[string]string, len(configs))
	for _, cfg := range configs {
		if cfg == nil || cfg.Color == "" {
			continue
		}
		colors[strings.ToLower(cfg.Name)] = cfg.Color
	}
	return colors
}

func buildTaskOwnerMap(tasks []*todo.Task) map[string]string {
	if len(tasks) == 0 {
		return nil
	}
	ownerMap := make(map[string]string, len(tasks))
	for _, t := range tasks {
		if t.Owner != "" {
			ownerMap[t.ID] = t.Owner
		}
	}
	if len(ownerMap) == 0 {
		return nil
	}
	return ownerMap
}
