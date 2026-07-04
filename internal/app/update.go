// Bubble Tea Update dispatch. Top-level switch on tea.Msg, with the
// overlay-selector list that determines which input layers are "active"
// for delegation. The actual handlers live in sibling files:
//
//	update_keys.go           keyboard handling (Ctrl-shortcuts, Tab,
//	                         Enter, history) + active-modal delegation
//	update_resize.go         window resize + scrollback reflow
//	update_submit.go         submit + provider turn + skill invocation
//	update_command.go        slash command deps + execution
//	update_modal.go          operation mode + question modal protocol
//	update_approval.go       permission approval flow + gate response
//	update_input_effects.go  stream cancel, tool-call cancel, image
//	                         paste, quit-with-cancel
package app

import (
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/log"
)

// overlayPanel is a UI element that, while active, takes over both the
// keyboard (consuming keypresses before the textarea sees them) and the
// screen. It covers the slash-command pickers (/model, /tools, /skills, ...)
// and the two docked modals (Question, Approval).
//
// overlayPanels is the single source of truth for which panel is in front,
// so key routing (routeKeypress) and rendering (viewString) can never
// disagree about who owns the foreground.
type overlayPanel interface {
	IsActive() bool
	HandleKeypress(tea.KeyMsg) tea.Cmd
	Render() string
}

// pasteHandler is the optional half of overlayPanel for text-entry dialogs.
// Bracketed paste arrives as tea.PasteMsg, which is not a tea.KeyMsg, so it
// never reaches HandleKeypress — it needs its own routing (see Update). An
// overlay that accepts pasted text implements this; one that doesn't has the
// paste dropped rather than leaking into the hidden prompt textarea behind it.
type pasteHandler interface {
	HandlePaste(content string) tea.Cmd
}

// overlayPanels lists every panel that may be in front, in keyboard-priority
// order: the docked modals win over the slash-command pickers. At most one is
// active at a time; activeOverlay returns the first that reports IsActive().
func (m *model) overlayPanels() []overlayPanel {
	return []overlayPanel{
		m.conv.Modal.Question, // docked modals (rendered between separators)
		&m.userInput.Approval,
		&m.userInput.Secret,
		&m.userInput.Provider.Selector, // fullscreen slash-command pickers
		&m.userInput.Tool,
		&m.userInput.Skill.Selector,
		&m.userInput.Agent,
		&m.userInput.Persona,
		&m.userInput.MCP.Selector,
		&m.userInput.Plugin,
		&m.userInput.Session.Selector,
		&m.userInput.Memory.Selector,
		&m.userInput.Search,
		&m.userInput.Config,
	}
}

// activeOverlay returns the foreground panel, if any.
func (m *model) activeOverlay() (overlayPanel, bool) {
	for _, ov := range m.overlayPanels() {
		if ov.IsActive() {
			return ov, true
		}
	}
	return nil, false
}

type initialPromptMsg string

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case initialPromptMsg:
		m.userInput.Textarea.SetValue(string(msg))
		return m, m.handleSubmit()
	case tea.KeyPressMsg:
		if c, ok := m.routeKeypress(msg); ok {
			return m, c
		}
	case tea.PasteMsg:
		// Route paste to the foreground overlay, mirroring routeKeypress.
		// When an overlay owns the screen but can't take paste, drop it so
		// it doesn't leak into the prompt textarea hidden behind it. With no
		// overlay up, fall through to the textarea's own paste handling.
		if ov, ok := m.activeOverlay(); ok {
			if ph, ok := ov.(pasteHandler); ok {
				return m, ph.HandlePaste(msg.Content)
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		return m, m.handleWindowResize(msg)
	case spinner.TickMsg:
		if m.needsSpinner() {
			var cmd tea.Cmd
			m.conv.Spinner, cmd = m.conv.Spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case ctrlOSingleTickMsg:
		return m, m.handleCtrlOSingleTick()
	case input.PromptSuggestionMsg:
		input.HandlePromptSuggestion(&m.userInput, m.conv.Stream.Active, m.userInput.Textarea.Value(), msg)
		return m, nil
	case kit.DismissedMsg:
		// Sync env state with llm.Default. Credential removal (Ctrl+D in
		// the Providers tab) clears the store's CurrentModel and updates
		// llm.Default — but the per-turn env fields would stay stale until
		// the next explicit model selection without this refresh.
		m.env.CurrentModel = m.services.LLM.CurrentModel()
		m.env.LLMProvider = m.services.LLM.Provider()
		return m, nil
	case input.ToolToggleMsg:
		// The selector already persisted the enable/disable; the tool registry
		// is read live, so there's nothing to do at the app level. Return
		// explicitly so the message doesn't leak into sub-model routing and
		// the textarea (unlike AgentToggleMsg/SkillCycleMsg below, which do
		// need a reaction).
		return m, nil
	case input.ConfigSavedMsg:
		// Refresh the in-memory settings handle so re-opening /config (and any
		// in-session reader) sees the just-saved values rather than the stale
		// pre-save snapshot. The panel already persisted to disk.
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after config save failed", zap.Error(err))
		}
		m.conv.AddNotice("Self-learning config saved (" + msg.Scope + ")")
		m.notifySelfLearnOverride(msg)
		// Re-wire the L1 reviewer so the just-saved arms / cadences take
		// effect on the running session instead of silently waiting for
		// the next agent restart. Wire only when the agent is already
		// active; an inactive session will wire on the first user turn.
		if m.services.Agent.Active() {
			m.wireSelfLearn(m.buildAgentParams(), "")
		}
		return m, nil
	case input.ThemeSavedMsg:
		// The panel already applied (kit.InitTheme) and persisted the theme;
		// refresh the in-memory handle so re-opening /config reflects it.
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after theme save failed", zap.Error(err))
		}
		m.conv.AddNotice("Theme set to " + msg.Theme)
		return m, nil
	case input.ContextBarSavedMsg:
		// Update the live display flag so the status line reflects the choice
		// immediately, then refresh the in-memory handle for re-opens.
		m.env.ShowContextBar = msg.On
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after context-bar save failed", zap.Error(err))
		}
		if msg.On {
			m.conv.AddNotice("Context bar on")
		} else {
			m.conv.AddNotice("Context bar off")
		}
		return m, nil
	case input.SkillCycleMsg:
		// Why re-emit on toggle: the skills directory rides in
		// <system-reminder>, which is only refreshed at SessionStart and
		// PostCompact. Without this nudge the LLM sees stale state until
		// one of those fires.
		m.services.Reminder.RequeueSystemReminders()
		return m, nil
	case input.AgentToggleMsg:
		// Why stop on toggle: the agents directory lives in the Agent tool's
		// description, which is frozen at agent build time. Stopping forces
		// ensureAgentSession to rebuild on the next user turn with the new
		// directory. Why guard on Stream.Active: stopping mid-stream would
		// orphan in-flight tool calls and the partial assistant turn —
		// leave the toggle pending; ensureAgentSession will see the updated
		// store the next time it actually rebuilds.
		if m.services.Agent.Active() && !m.conv.Stream.Active {
			m.services.Agent.Stop()
		}
		return m, nil
	case persistSessionDoneMsg:
		if msg.err != nil {
			log.Logger().Warn("async session persist failed", zap.Error(msg.err))
		}
		return m, nil
	case flushResultMsg:
		return m, m.handleFlushResult(msg)
	case conv.QuestionResponseMsg:
		return m, m.handleQuestionResponse(msg)
	case input.SecretPromptResponseMsg:
		return m, m.handleSecretPromptResponse(msg)
	case input.ApprovalResponseMsg:
		return m, m.handlePermGateDecision(permissionDecision{
			Approved: msg.Approved, AllowAll: msg.AllowAll, Persist: msg.Persist, Request: msg.Request,
		})
	case stopHookResultMsg:
		return m, m.handleStopHookResult(msg)
	case mainEventMsg:
		return m, m.onMainEvent(msg.event)
	case selflearnTickMsg:
		return m, m.handleSelflearnTick()
	}

	if cmd, handled := m.routeToSubModel(msg); handled {
		return m, cmd
	}
	return m, m.updateTextarea(msg)
}

// routeToSubModel hands a non-keyboard tea.Msg to the first sub-model
// that claims it. Order matters: conv (agent outbox events) goes first
// because its events are the most frequent; trigger (cron/file watcher)
// goes last because it primarily produces messages, doesn't consume
// them. Returns (cmd, true) if any sub-model handled the message.
func (m *model) routeToSubModel(msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := conv.Update(m, &m.conv, msg); ok {
		return cmd, true
	}
	if cmd, ok := m.updateMode(msg); ok {
		return cmd, true
	}
	if cmd, ok := input.Update(m.overlayDeps(), msg); ok {
		return cmd, true
	}
	if cmd, ok := trigger.Update(m.triggerDeps(), &m.systemInput, msg); ok {
		return cmd, true
	}
	return nil, false
}

func (m *model) needsSpinner() bool {
	return m.conv.Stream.Active ||
		m.conv.Compact.Active ||
		m.userInput.Provider.FetchingLimits ||
		m.services.Tracker.HasInProgress()
}

func (m *model) updateTextarea(msg tea.Msg) tea.Cmd {
	cmd, changed := m.userInput.HandleTextareaUpdate(msg)
	if changed {
		m.userInput.PromptSuggestion.Clear()
	}
	return cmd
}
