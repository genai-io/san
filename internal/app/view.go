// Bubble Tea View: composes the terminal UI from active content, input area, and status bar.
package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/gen-code/internal/app/conv"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/task/tracker"
)

var ghostTextStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)

// View dispatches to one of four layouts, top-down:
//
//  1. Loading splash (env not ready yet)
//  2. Active popup (slash-command picker / etc.) — fullscreen
//  3. Active modal (Question / Approval) — wrapped between separators
//  4. Normal mode — chat section + status + input strip
func (m *model) View() string {
	if !m.env.Ready {
		return "\n  Loading..."
	}
	if popupView := m.renderActivePopup(); popupView != "" {
		return popupView
	}

	separator := conv.SeparatorStyle.Render(strings.Repeat("─", m.env.Width))
	trackerView := m.renderTrackerList()
	trackerPrefix := ""
	if trackerView != "" {
		trackerPrefix = "\n" + strings.TrimSuffix(trackerView, "\n") + "\n"
	}

	if modalView := m.renderActiveModal(separator, trackerPrefix); modalView != "" {
		return modalView
	}
	return m.renderNormalView(separator, trackerView)
}

// renderNormalView composes the standard layout: chat scrollback area,
// turn-usage summary, queue preview, textarea + suggestions, and the
// bottom status line.
//
// The chat section is height-limited so the bubbletea View() output never
// exceeds the terminal height. When live content is taller than the
// available space, conv.ScrollWindow shows a viewport into it (the latest
// content by default, or an earlier window when the user has scrolled up).
// The full, untruncated content is committed to terminal scrollback via
// CommitMessages / tea.Println, so the user can scroll up to see everything
// once the response completes.
func (m *model) renderNormalView(separator, trackerView string) string {
	// Build the bottom chrome first so we can measure how many lines it
	// consumes and subtract from the terminal height.
	bottomChrome := m.buildBottomChrome(separator)
	bottomLines := strings.Count(bottomChrome, "\n")

	maxContentHeight := 0
	// Only truncate when there's room for at least one line of content.
	if m.env.Height > bottomLines {
		maxContentHeight = m.env.Height - bottomLines
	}

	// Render ALL messages (both committed and active) so the scrollable
	// viewport has the full conversation to navigate.
	allContent := conv.RenderMessageRange(m.messageRenderParams(), 0, len(m.conv.Messages), m.conv.Stream.Active)
	chatSection := m.renderChatSection(allContent, trackerView)

	// Window the content to the available height. ScrollWindow is the single
	// place scroll bounds are enforced: it returns the offset clamped to the
	// valid range, which we store back as the canonical position so a
	// wheel-up past the top can't accumulate an unbounded offset.
	windowed, clamped := conv.ScrollWindow(strings.Split(chatSection, "\n"), m.conv.ContentOffset, maxContentHeight)
	m.conv.ContentOffset = clamped

	return windowed + bottomChrome
}

// buildBottomChrome renders everything below the chat section (turn usage,
// separators, queue preview, input area, suggestions, status line) into a
// single string so its line count can be measured.
func (m *model) buildBottomChrome(separator string) string {
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

func (m *model) renderActivePopup() string {
	for _, s := range m.popups() {
		if s.IsActive() {
			return s.Render()
		}
	}
	return ""
}

func (m *model) renderActiveModal(separator, trackerPrefix string) string {
	switch {
	case m.userInput.Approval.IsActive():
		return separatorWrapped(trackerPrefix, separator, m.userInput.Approval.Render())
	case m.conv.Modal.Question.IsActive():
		return separatorWrapped(trackerPrefix, separator, m.conv.Modal.Question.Render())
	default:
		return ""
	}
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

// renderChatSection assembles the full chat content (active messages, tracker,
// transient spinners) into a single string. It is pure: height-limiting and
// scroll windowing are applied by the caller via conv.ScrollWindow.
func (m model) renderChatSection(content, trackerView string) string {
	var parts []string

	if content != "" {
		parts = append(parts, content)
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

	return strings.Join(parts, "\n")
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
	})
}

func (m model) renderModeStatus() string {
	modelName := m.env.GetModelID()
	thinkingEffort := m.env.EffectiveThinkingEffort()
	showThinking := true
	if m.env.CurrentModel != nil && m.env.CurrentModel.Provider == llm.OpenAI && thinkingEffort != "" {
		modelName += " (" + thinkingEffort + ")"
		showThinking = false
	}
	if m.services.Hook != nil {
		if status := m.services.Hook.CurrentStatusMessage(); status != "" {
			modelName = status
		}
	}
	return conv.RenderModeStatus(conv.OperationModeParams{
		Mode:             conv.OperationMode(m.env.OperationMode),
		InputTokens:      m.env.InputTokens,
		OutputTokens:     m.env.OutputTokens,
		InputLimit:       kit.GetEffectiveInputLimit(m.services.LLM.Store(), m.env.CurrentModel),
		ModelName:        modelName,
		StatusMessage:    m.userInput.Provider.StatusMessage,
		ConversationCost: m.env.ConversationCost,
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
		Blink:        m.conv.Blink,
		ModelName:    m.env.GetModelID(),
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
	if m.services.Subagent == nil {
		return nil
	}
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

func buildTaskOwnerMap(tasks []*tracker.Task) map[string]string {
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
