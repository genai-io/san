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
	"strconv"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/todo"
)

// overlayPanel is a UI element that, while active, takes over both the
// keyboard (consuming keypresses before the textarea sees them) and the
// screen. It covers the slash-command pickers (/models, /tools, /skills, ...)
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

// resizableOverlay is implemented by fullscreen overlays whose layout caches
// the terminal dimensions captured when they open. WindowSizeMsg must refresh
// those dimensions or a narrower terminal will hard-wrap stale-width frames,
// leaving fragments from adjacent rows on screen.
type resizableOverlay interface {
	Resize(width, height int)
}

// pasteHandler is the optional half of overlayPanel for text-item dialogs.
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
		&m.userInput.Settings,
		&m.userInput.Autopilot,
		&m.userInput.Evolve,
		&m.userInput.Transcript,
		&m.userInput.Tasks,
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

// Update wraps the dispatch below so a few things are observed across every
// branch of it: an overlay closing, which is what restarts a held scrollback
// print — whichever of the sixteen panels it was, and however it was
// dismissed — and, in a group, the loop starting and this member's state.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Cancellation can dismiss a modal before dispatch, so observe it first.
	_, overlayWasActive := m.activeOverlay()
	m.pruneInteractions()
	model, cmd := m.dispatch(msg)
	if refresh := m.syncGitBar(); refresh != nil {
		cmd = tea.Batch(cmd, refresh)
	}
	if print := m.flushVisibleOverflow(); print != nil {
		cmd = tea.Batch(cmd, print)
	}
	if poll := m.startGroupPolling(); poll != nil {
		cmd = tea.Batch(cmd, poll)
	}
	m.syncGroupState()
	if next := m.advanceInteractions(); next != nil {
		cmd = tea.Batch(cmd, next)
	}
	m.syncTaskViewerSession()
	if tick := m.taskViewerTick(); tick != nil {
		cmd = tea.Batch(cmd, tick)
	}
	// Advancing can also dismiss a request canceled during this Update.
	if _, overlayIsActive := m.activeOverlay(); overlayWasActive && !overlayIsActive {
		if resume := m.resumeDeferredScrollbackPrint(); resume != nil {
			cmd = tea.Batch(cmd, resume)
		}
	}
	switch msg.(type) {
	case conv.InteractionRequestMsg, conv.AgentActivityBatchMsg, conv.AgentActivityMsg, conv.AgentToUITickMsg:
		cmd = tea.Batch(cmd, m.conv.AgentToUI.Check())
	}
	if m.flush.output != nil && m.flush.frameForPrint == nil {
		// Surfaces a failed terminal write; an insert's batch is released when
		// its print finishes, so nothing is held here.
		if err := m.flush.output.Flush(); err != nil {
			return model, tea.Quit
		}
	}
	return model, cmd
}

func (m *model) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case conv.PermGateMsg:
		if msg.Gate != nil && msg.Gate != m.services.Agent.PermissionGate() {
			return m, nil
		}
		return m, m.HandlePermGate(msg.Request)
	case initialPromptMsg:
		m.userInput.Textarea.SetValue(string(msg))
		return m, m.handleSubmit()
	case updateInstalledMsg:
		m.updateInstalled = string(msg)
		return m, nil
	case updateFailedMsg:
		m.updateFailed = msg
		return m, nil
	case gitStatusMsg:
		return m, m.handleGitStatus(msg)
	case gitBarTickMsg:
		return m, m.handleGitBarTick(msg)
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
		// The /autopilot Mission dialog runs its own spinner while awaiting a
		// reply. Ticks carry a per-spinner id, so a foreign tick returns a nil
		// cmd here and falls through to the conversation spinner below.
		if m.userInput.Autopilot.Thinking() {
			if cmd := m.userInput.Autopilot.UpdateSpinner(msg); cmd != nil {
				return m, cmd
			}
		}
		if m.needsSpinner() {
			var cmd tea.Cmd
			m.conv.Spinner, cmd = m.conv.Spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	case ctrlOSingleTickMsg:
		return m, m.handleCtrlOSingleTick()
	case historyLoadedMsg:
		m.historyLoaded(msg)
		return m, nil
	case input.TaskStopMsg:
		return m, m.stopViewedTask(msg)
	case input.TaskContinueMsg:
		return m, m.continueViewedTask(msg)
	case input.TaskReviewApprovalMsg:
		return m, m.reviewTaskApproval(msg)
	case input.TaskViewerClosedMsg:
		if m.consumeTaskAction(msg.ViewID) {
			m.hideTaskViewer()
		}
		return m, m.advanceInteractions()
	case taskViewerTickMsg:
		if msg.sessionID == m.tasks.sessionID && msg.viewID == m.tasks.pollID {
			m.tasks.pollID = 0
			m.refreshTaskViewer()
		}
		return m, nil
	case taskStopFinishedMsg:
		if msg.sessionID == m.sessionID() && msg.sessionID == m.tasks.sessionID && msg.viewID == m.tasks.stopID {
			if msg.err != nil {
				m.conv.AddNotice("Could not stop task " + msg.taskID + ": " + msg.err.Error())
			}
			// The worker's terminal notice owns the outcome; do not print a
			// second success receipt for the same stop operation.
			m.resumeTaskViewer()
			return m, tea.Batch(m.CommitMessages()...)
		}
		return m, nil
	case ctrlCExpiredMsg:
		// A later press re-armed the window; its own tick will disarm it.
		if time.Since(m.userInput.LastCtrlC) >= ctrlCExitWindow {
			m.userInput.LastCtrlC = time.Time{}
		}
		return m, nil
	case input.PromptSuggestionMsg:
		// A request can finish just after the user switched Suggest off. The
		// setting is the final authority, so a stale completion cannot restore
		// ghost text after Clear cancelled it.
		if !m.env.AutoPilot.Steers.SuggestOn() {
			m.userInput.PromptSuggestion.Clear()
			return m, nil
		}
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
		// The selector already persisted the enable/disable to disk. Refresh the
		// in-memory settings so DisabledTools() (read by the built-in and MCP
		// tool filters at agent-build time) reflects it; ensureAgentRuntime then
		// rebuilds on the next turn when the disabled set drifts, so the toggle
		// takes effect without interrupting an in-flight turn.
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after tool toggle failed", zap.Error(err))
		}
		return m, nil
	case memberMsg:
		return m, m.handleMemberMsg(msg)
	case groupJoinMsg:
		m.conv.AddNotice(m.finishJoin(msg))
		return m, tea.Batch(m.CommitMessages()...)
	case input.MissionRefinedMsg:
		// The /autopilot Mission editor's refined text arrived; hand it to the
		// panel to replace the draft (or surface an error under the editor).
		m.userInput.Autopilot.DeliverRefinedMission(msg.Mission, msg.Err)
		return m, nil
	case autopilotDecisionMsg:
		// The TurnEnd steer's continue/stop verdict came back.
		return m, m.handleAutopilotDecision(msg)
	case autopilotQuestionMsg:
		// The Question steer's answer (or defer) came back.
		return m, m.handleAutopilotQuestion(msg)
	case autopilotRecoverMsg:
		// The backoff after a failed turn elapsed; see whether the run can resume.
		return m, m.handleAutopilotRecover(msg)
	case autopilotModeSettledMsg:
		// The mode has rested on AutoPilot long enough to open it hands-free.
		return m, m.handleAutopilotModeSettled()
	case input.AutopilotSavedMsg:
		// Apply the edit to the live session (persists/resumes with the session),
		// hot-swap the running judge so the new model/prompt/steers take effect at
		// once, and write it — minus the per-session mission and system prompt — as
		// the default for new sessions.
		before := m.env.AutoPilot
		m.applyAutopilotConfig(msg.Config)
		m.persistAutopilotDefault()
		// Suggest is the input hint's feature switch. Clear both visible and
		// in-flight hints immediately when it is turned off.
		if !m.env.AutoPilot.Steers.SuggestOn() {
			m.userInput.PromptSuggestion.Clear()
		}
		if m.autopilotEngaged() || !drivingChanged(before, msg.Config) {
			m.conv.AddNotice("Autopilot config saved")
		} else {
			m.conv.AddNotice("Autopilot config saved · shift+tab to engage")
		}
		return m, nil
	case input.GoalSetMsg:
		// /goal: the stated goal becomes the mission and the copilot drives.
		return m, m.startGoal(msg.Goal)
	case input.GoalClearedMsg:
		// /goal clear: stand the copilot down.
		m.clearGoal()
		return m, nil
	case input.ConfigSavedMsg:
		// Refresh the in-memory settings handle so re-opening /settings (and any
		// in-session reader) sees the just-saved values rather than the stale
		// pre-save snapshot. The panel already persisted to disk.
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after config save failed", zap.Error(err))
		}
		m.conv.AddNotice("Self-learning config saved (" + string(msg.Scope) + ")")
		m.notifySelfLearnOverride(msg)
		// Re-wire the L1 reviewer so the saved values take effect on the
		// running session. If the save also changed the Evolve tool's
		// capabilities, the toolset is reconciled when the next turn starts —
		// ensureAgentRuntime rebuilds on capability drift (agentEvolveCaps),
		// which covers external settings edits the same way.
		if m.services.Agent.Active() {
			params, err := m.buildAgentParams()
			if err != nil {
				log.Logger().Warn("could not configure self-learning", zap.Error(err))
			} else {
				m.wireSelfLearn(params)
			}
		}
		return m, nil
	case input.ThemeSavedMsg:
		// The panel already applied (kit.InitTheme) and persisted the theme;
		// refresh the in-memory handle so re-opening /settings reflects it.
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
	case input.GitBarSavedMsg:
		m.env.ShowGitBar = msg.On
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after Git-bar save failed", zap.Error(err))
		} else {
			m.env.ShowGitBar = m.services.Setting.Snapshot().ShowGitBar()
		}
		state := "off"
		if m.env.ShowGitBar {
			state = "on"
		}
		if m.env.ShowGitBar != msg.On {
			m.conv.AddNotice("Saved, but a project setting keeps Git bar " + state)
		} else {
			m.conv.AddNotice("Git bar " + state)
		}
		return m, nil
	case input.ThinkingDisplaySavedMsg:
		// Update the live render flag so the tail and the scrollback flush
		// reflect the choice immediately, then refresh the in-memory handle for
		// re-opens. Already-committed reasoning in native scrollback is
		// immutable (ADR-0002), so this applies from here on.
		m.env.ThinkingDisplay = msg.Mode
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after thinking-display save failed", zap.Error(err))
		}
		m.conv.AddNotice("Thinking: " + msg.Mode)
		return m, nil
	case input.BashPreviewSavedMsg:
		// Applies to rows drawn from here on; committed scrollback is immutable.
		m.env.BashPreviewLines = msg.Lines
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after shell-preview save failed", zap.Error(err))
		}
		preview := "off"
		switch {
		case msg.Lines == 1:
			preview = "1 line"
		case msg.Lines > 1:
			preview = strconv.Itoa(msg.Lines) + " lines"
		}
		m.conv.AddNotice("Shell output preview: " + preview)
		return m, nil
	case input.AutoUpdateSavedMsg:
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			log.Logger().Warn("reload settings after auto-update save failed", zap.Error(err))
		}
		// Report the effective value: a project-level autoUpdate outranks the
		// user level the panel writes.
		on := m.services.Setting.AutoUpdate()
		state := "off"
		if on {
			state = "on"
		}
		if on != msg.On {
			m.conv.AddNotice("Saved, but a project setting keeps auto update " + state)
		} else {
			m.conv.AddNotice("Auto update " + state + " — takes effect next launch")
		}
		return m, nil
	case input.AllowBypassSavedMsg:
		if err := m.services.Setting.Reload(m.env.CWD); err != nil {
			// The in-memory handle still holds the pre-save value, and it is
			// what cycleOperationMode consults — so a lock that cannot be
			// re-read has not taken effect. Say so instead of claiming it did.
			log.Logger().Warn("reload settings after YOLO-mode save failed", zap.Error(err))
			m.conv.AddNotice("Saved, but couldn't re-read settings — restart for it to take effect")
			return m, nil
		}
		// Reason about the effective setting, not the payload: the panel writes
		// the user level, but a project-level allowBypass outranks it. Demoting
		// on msg.Allowed alone would kick a session out of YOLO mode that
		// shift+tab can walk straight back into.
		allowed := m.services.Setting.AllowBypass()
		if !allowed && m.env.OperationMode == setting.ModeBypassPermissions {
			// persistOperationMode too, or lastOperationMode stays "bypass" and
			// the next launch restores YOLO behind the user's back.
			m.env.OperationMode = setting.ModeNormal
			m.applyOperationMode()
			m.persistOperationMode()
		}
		// Report what is in force, not what was written. Either direction can
		// be outranked by the project level, and saying "allowed" when
		// shift+tab still stops at autopilot is the same lie as the reverse.
		switch {
		case msg.Allowed && allowed:
			m.conv.AddNotice("YOLO mode allowed — shift+tab to reach it")
		case msg.Allowed:
			m.conv.AddNotice("Allowed for your user, but project settings still lock YOLO mode")
		case allowed:
			m.conv.AddNotice("Locked for your user, but project settings still allow YOLO mode")
		default:
			m.conv.AddNotice("YOLO mode locked")
		}
		return m, nil
	case input.SkillCycleMsg:
		// Why re-emit on toggle: the skills directory rides in
		// <system-reminder>, which is sent once per conversation. Without
		// this nudge the LLM sees stale state until the next one.
		m.services.Reminder.RequeueSystemReminders()
		return m, nil
	case input.AgentToggleMsg:
		// Why stop on toggle: the agents directory lives in the Agent tool's
		// description, which is frozen at agent build time. Stopping forces
		// ensureAgentRuntime to rebuild on the next user turn with the new
		// directory. Why guard on Stream.Active: stopping mid-stream would
		// orphan in-flight tool calls and the partial assistant turn —
		// leave the toggle pending; ensureAgentRuntime will see the updated
		// store the next time it actually rebuilds.
		if m.services.Agent.Active() && !m.conv.Stream.Active {
			m.StopAgentRuntime()
		}
		return m, nil
	case agentSendFailedMsg:
		log.Logger().Warn("failed to send message to agent", zap.Error(msg.err))
		if m.conv.Stream.Waiting {
			m.conv.Stream.Stop()
		}
		m.conv.AddNotice("Failed to send message: " + msg.err.Error())
		return m, tea.Batch(m.CommitMessages()...)
	case flushResultMsg:
		return m, m.handleFlushResult(msg)
	case scrollbackPrintReadyMsg:
		// Hold prints while an overlay is up: a full-screen panel draws on the
		// alternate screen, where a Println vanishes with the buffer, and a docked
		// modal draws inline, where insertAbove would carry the prompt into
		// scrollback with it. Keep the queued payload intact and restart it when
		// the overlay closes.
		if _, active := m.activeOverlay(); active {
			return m, nil
		}
		return m, m.startScrollbackPrint(msg.id)
	case scrollbackPrintDoneMsg:
		return m, m.finishScrollbackPrint(msg.id)
	case tea.ScrollUpMsg:
		return m, m.finishChunkScroll(msg)
	case tea.ResizeScrollbackMsg:
		return m, m.finishResize(msg)
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
	case mainNoticeMsg:
		return m, m.onMainNotice(msg.notice)
	case btwAnswerMsg:
		return m, m.onBtwAnswer(msg)
	case tea.BlurMsg:
		return m, m.onBlur()
	case tea.FocusMsg:
		return m, m.onFocus()
	case recapPrepareMsg:
		if msg.awaySince.Equal(m.unfocusedSince) {
			return m, m.prepareRecap()
		}
		return m, nil
	case recapReadyMsg:
		return m, m.onRecapReady(msg)
	case selfLearnStartedMsg:
		return m, m.onSelfLearnStarted()
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

// needsSpinner reports whether any animation should still be running.
//
// Every term is live in-memory runtime state. Persisted tracker status is
// deliberately absent: nothing obliges its writer to clear it, so keying the
// tick loop off it let the loop re-arm itself forever after the turn ended.
func (m *model) needsSpinner() bool {
	return m.conv.Stream.Active ||
		m.conv.Compact.Active ||
		m.btwWaiting() ||
		m.recap.show ||
		m.services.Task.HasRunning()
}

// executingTrackerItem reports whether the executor behind a tracker item is
// running right now. A worker item names a background task, so todo resolves
// it against this session's task manager. A plan item authored by the model has no
// executor of its own — the main agent loop advances it, so it runs exactly
// while that loop streams.
func (m *model) executingTrackerItem(t *todo.Item) bool {
	if todo.BackgroundTaskID(t) == "" {
		return m.conv.Stream.Active
	}
	if m.services.Task == nil {
		return false
	}
	worker, known := m.services.Task.Get(todo.BackgroundTaskID(t))
	return known && worker.IsRunning()
}

func (m *model) updateTextarea(msg tea.Msg) tea.Cmd {
	cmd, changed := m.userInput.HandleTextareaUpdate(msg)
	if changed {
		m.userInput.PromptSuggestion.Clear()
	}
	return cmd
}
