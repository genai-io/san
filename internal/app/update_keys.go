// Keyboard handling: routes a tea.KeyMsg first to any active modal, then
// to image/suggestion/queue overlays, then to the textarea-level shortcuts
// (Ctrl+C/D/L/E/O, Tab, Enter, etc.). Also owns the Ctrl+O double-tap
// detection and the per-keystroke thinking-effort cycle.
package app

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/v2/internal/app/kit"
	"github.com/genai-io/san/v2/internal/llm"
)

const ctrlODoubleTapWindow = 300 * time.Millisecond

type ctrlOSingleTickMsg struct{}

// routeKeypress is the priority dispatcher for tea.KeyMsg. A keypress
// flows through these layers in order; the first one that claims it wins:
//
//  1. tryActivePopup           — any open modal or slash-command picker
//  2. HandleImageSelectKey     — image picker overlay inside textarea
//  3. HandleSuggestionKey      — prompt-suggestion overlay inside textarea
//  4. HandleQueueSelectKey     — queue-navigation mode inside textarea
//  5. handleTextareaShortcut   — Ctrl-shortcuts + Tab/Enter/history
//
// Returns (cmd, true) if any layer consumed the key. Falling off the end
// means "let the textarea consume it as text" — that's handled in
// updateTextarea, not here.
func (m *model) routeKeypress(msg tea.KeyMsg) (tea.Cmd, bool) {
	if active, cmd := m.tryActivePopup(msg); active {
		return cmd, true
	}

	if c, ok := m.userInput.HandleImageSelectKey(msg); ok {
		return c, ok
	}
	if c, ok := m.userInput.HandleSuggestionKey(msg); ok {
		return c, ok
	}
	if c, ok := m.userInput.HandleQueueSelectKey(msg); ok {
		return c, ok
	}

	return m.handleTextareaShortcut(msg)
}

// handleTextareaShortcut handles keys that target the textarea itself:
// Ctrl-shortcuts (C/D/L/E/O/U/V/Y/T), Tab, Shift+Tab, Enter, Esc, and
// arrow-key history navigation. Returns (cmd, true) if the key was a
// recognized shortcut, (nil, false) to let the rune fall through to
// updateTextarea as plain text input.
func (m *model) handleTextareaShortcut(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyTab, tea.KeyRight:
		if m.userInput.PromptSuggestion.Text != "" && m.userInput.Textarea.Value() == "" {
			m.userInput.Textarea.SetValue(m.userInput.PromptSuggestion.Text)
			m.userInput.Textarea.CursorEnd()
			m.userInput.PromptSuggestion.Clear()
			return nil, true
		}

	case tea.KeyShiftTab:
		if !m.conv.Stream.Active && !m.userInput.Approval.IsActive() &&
			!m.conv.Modal.Question.IsActive() &&
			!m.userInput.Provider.Selector.IsActive() && !m.userInput.Suggestions.IsVisible() {
			m.cycleOperationMode()
			return nil, true
		}

	case tea.KeyCtrlT:
		return m.cycleThinkingEffort(), true

	case tea.KeyRunes:
		if msg.Alt && len(msg.Runes) == 1 && (msg.Runes[0] == 't' || msg.Runes[0] == 'T') {
			m.conv.ShowTasks = !m.conv.ShowTasks
			return nil, true
		}

	case tea.KeyCtrlO:
		return m.handleCtrlO(), true

	case tea.KeyCtrlE:
		return m.expandCollapseAll(), true

	case tea.KeyCtrlX:
		return nil, false

	case tea.KeyCtrlU:
		if m.userInput.Queue.Len() > 0 {
			m.userInput.Queue.Clear()
			return nil, true
		}
		return nil, false

	case tea.KeyCtrlV, tea.KeyCtrlY:
		return m.pasteImageFromClipboard()

	case tea.KeyCtrlC:
		if m.userInput.Textarea.Value() != "" {
			m.userInput.Reset()
			m.userInput.History.Index = -1
			m.userInput.LastCtrlC = time.Time{}
			return nil, true
		}
		if m.conv.Stream.Active {
			m.userInput.LastCtrlC = time.Time{}
			return m.handleStreamCancel(), true
		}
		now := time.Now()
		if !m.userInput.LastCtrlC.IsZero() && now.Sub(m.userInput.LastCtrlC) < 1*time.Second {
			return m.QuitWithCancel()
		}
		m.userInput.LastCtrlC = now
		_, cmd, _ := m.executeCommand(context.Background(), "/clear")
		return cmd, true

	case tea.KeyCtrlD:
		if m.userInput.Textarea.Value() != "" {
			return nil, false
		}
		return m.QuitWithCancel()

	case tea.KeyCtrlL:
		_, cmd, _ := m.executeCommand(context.Background(), "/clear")
		return cmd, true

	case tea.KeyEsc:
		if m.userInput.PromptSuggestion.Text != "" {
			m.userInput.PromptSuggestion.Clear()
			return nil, true
		}
		if m.userInput.Suggestions.IsVisible() {
			m.userInput.Suggestions.Hide()
			return nil, true
		}
		if m.conv.Stream.Active {
			return m.handleStreamCancel(), true
		}
		return nil, true

	case tea.KeyUp:
		if m.userInput.Textarea.Line() == 0 {
			if m.userInput.Queue.Len() > 0 {
				m.userInput.EnterQueueSelection()
				return nil, true
			}
			m.userInput.HistoryUp()
			return nil, true
		}

	case tea.KeyDown:
		lines := strings.Count(m.userInput.Textarea.Value(), "\n")
		if m.userInput.Textarea.Line() == lines {
			if m.userInput.Queue.Len() > 0 {
				m.userInput.EnterQueueSelection()
				return nil, true
			}
			m.userInput.HistoryDown()
			return nil, true
		}

	case tea.KeyEnter:
		if msg.Alt {
			m.userInput.Textarea.InsertString("\n")
			m.userInput.UpdateHeight()
			return nil, true
		}
		return m.handleSubmit(), true
	}

	return nil, false
}

func (m *model) cycleThinkingEffort() tea.Cmd {
	current := m.env.EffectiveThinkingEffort()
	next, ok := llm.NextThinkingEffort(m.env.LLMProvider, m.env.GetModelID(), current)
	if !ok {
		token := m.userInput.Provider.SetStatusMessage("reasoning is not supported by this provider")
		return kit.StatusTimer(3*time.Second, token)
	}

	m.env.SetThinkingEffort(next)
	status := "thinking: " + next
	if current != "" && current == next {
		status += " (only supported)"
	}
	token := m.userInput.Provider.SetStatusMessage(status)
	return kit.StatusTimer(3*time.Second, token)
}

// tryActivePopup hands the keypress to whichever popup is currently
// visible — the question modal, the approval modal, or one of the
// slash-command pickers (provider, tools, skills, ...). At most one
// popup is active at a time. Returns (true, cmd) if a popup consumed
// the key.
func (m *model) tryActivePopup(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.conv.Modal.Question.IsActive() {
		cmd, resp := m.conv.Modal.Question.HandleKeypress(msg)
		if resp != nil {
			return true, tea.Batch(cmd, m.handleQuestionResponse(*resp))
		}
		return true, cmd
	}
	if m.userInput.Approval.IsActive() {
		cmd, resp := m.userInput.Approval.HandleKeypress(msg)
		if resp != nil {
			return true, tea.Batch(cmd, m.handlePermBridgeDecision(permissionDecision{
				Approved: resp.Approved, AllowAll: resp.AllowAll, Persist: resp.Persist, Request: resp.Request,
			}))
		}
		return true, cmd
	}
	for _, s := range m.popups() {
		if s.IsActive() {
			return true, s.HandleKeypress(msg)
		}
	}

	return false, nil
}

func (m *model) handleCtrlO() tea.Cmd {
	if m.userInput.Approval.IsActive() {
		m.userInput.Approval.TogglePreview()
		return nil
	}

	now := time.Now()
	if !m.userInput.LastCtrlO.IsZero() && now.Sub(m.userInput.LastCtrlO) < ctrlODoubleTapWindow {
		m.userInput.LastCtrlO = time.Time{}
		return m.expandCollapseAll()
	}

	m.userInput.LastCtrlO = now
	return tea.Tick(ctrlODoubleTapWindow, func(time.Time) tea.Msg {
		return ctrlOSingleTickMsg{}
	})
}

func (m *model) handleCtrlOSingleTick() tea.Cmd {
	if m.userInput.LastCtrlO.IsZero() {
		return nil
	}
	m.userInput.LastCtrlO = time.Time{}
	m.conv.ToggleMostRecentExpandable()
	return m.reflowScrollback()
}

func (m *model) expandCollapseAll() tea.Cmd {
	m.conv.ToggleAllExpandable()
	return m.reflowScrollback()
}
