// Bubble Tea Update: top-level message dispatch, key routing, input side effects,
// submit flow, approval flow, permission bridge, and mode handling.
package app

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/app/conv"
	"github.com/genai-io/gen-code/internal/app/input"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/app/trigger"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/image"
	"github.com/genai-io/gen-code/internal/llm"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/session"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/tool"
	"github.com/genai-io/gen-code/internal/tool/perm"
)

// ============================================================
// Update dispatch and routing
// ============================================================

type overlaySelector interface {
	IsActive() bool
	HandleKeypress(tea.KeyMsg) tea.Cmd
	Render() string
}

func (m *model) overlaySelectors() []overlaySelector {
	return []overlaySelector{
		&m.userInput.Provider.Selector,
		&m.userInput.Tool,
		&m.userInput.Skill.Selector,
		&m.userInput.Agent,
		&m.userInput.MCP.Selector,
		&m.userInput.Plugin,
		&m.userInput.Session.Selector,
		&m.userInput.Memory.Selector,
		&m.userInput.Search,
		&m.userInput.Identity,
	}
}

type initialPromptMsg string

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case initialPromptMsg:
		m.userInput.Textarea.SetValue(string(msg))
		return m, m.handleSubmit()
	case tea.KeyMsg:
		if c, ok := m.handleKeypress(msg); ok {
			return m, c
		}
	case tea.WindowSizeMsg:
		return m, m.handleWindowResize(msg)
	case spinner.TickMsg:
		if m.needsSpinner() {
			var cmd tea.Cmd
			m.conv.Spinner, cmd = m.conv.Spinner.Update(msg)
			m.conv.Blink++
			return m, cmd
		}
		return m, nil
	case ctrlOSingleTickMsg:
		return m, m.handleCtrlOSingleTick()
	case input.PromptSuggestionMsg:
		input.HandlePromptSuggestion(&m.userInput, m.conv.Stream.Active, m.userInput.Textarea.Value(), msg)
		return m, nil
	case kit.DismissedMsg, input.ToolToggleMsg, input.SkillCycleMsg, input.AgentToggleMsg:
		return m, nil
	case persistSessionDoneMsg:
		if msg.err != nil {
			log.Logger().Warn("async session persist failed", zap.Error(msg.err))
		}
		return m, nil
	case stopHookResultMsg:
		return m, m.handleStopHookResult(msg)
	}

	if cmd, handled := m.routeFeatureUpdate(msg); handled {
		return m, cmd
	}
	return m, m.updateTextarea(msg)
}

func (m *model) routeFeatureUpdate(msg tea.Msg) (tea.Cmd, bool) {
	if cmd, ok := conv.Update(m, &m.conv, msg); ok {
		return cmd, true
	}
	if cmd, ok := input.UpdateApproval(m.approvalDeps(), msg); ok {
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

// ============================================================
// Key dispatch
// ============================================================

func (m *model) handleKeypress(msg tea.KeyMsg) (tea.Cmd, bool) {
	if active, cmd := m.delegateToActiveModal(msg); active {
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

	return m.handleInputKey(msg)
}

func (m *model) handleInputKey(msg tea.KeyMsg) (tea.Cmd, bool) {
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
		if m.userInput.Queue.PendingCount() > 0 {
			m.userInput.Queue.Clear()
			m.userInput.Queue.SelectIdx = -1
			m.userInput.Queue.Stashed = ""
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

	m.env.ThinkingEffort = next
	status := "thinking: " + next
	if current != "" && current == next {
		status += " (only supported)"
	}
	token := m.userInput.Provider.SetStatusMessage(status)
	return kit.StatusTimer(3*time.Second, token)
}

func (m *model) delegateToActiveModal(msg tea.KeyMsg) (bool, tea.Cmd) {
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
			return true, tea.Batch(cmd, m.handlePermBridgeDecision(permissionDecision{Approved: resp.Approved, AllowAll: resp.AllowAll, Request: resp.Request}))
		}
		return true, cmd
	}
	for _, s := range m.overlaySelectors() {
		if s.IsActive() {
			return true, s.HandleKeypress(msg)
		}
	}

	return false, nil
}

// ============================================================
// Key handlers: Ctrl+O, expand/collapse, window resize, scrollback
// ============================================================

const ctrlODoubleTapWindow = 300 * time.Millisecond

type ctrlOSingleTickMsg struct{}

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

func (m *model) handleWindowResize(msg tea.WindowSizeMsg) tea.Cmd {
	oldWidth := m.env.Width
	m.env.Width = msg.Width
	m.env.Height = msg.Height
	m.userInput.TerminalHeight = msg.Height

	m.conv.ResizeMDRenderer(msg.Width)

	if !m.env.Ready {
		m.env.Ready = true

		var cmds []tea.Cmd
		if len(m.conv.Messages) > 0 {
			cmds = append(cmds, m.commitAllMessages()...)
		} else {
			cmds = append(cmds, tea.Println(conv.RenderWelcome()))
		}

		if m.userInput.Session.PendingSelector {
			m.userInput.Session.PendingSelector = false
			if m.services.Session.GetStore() != nil {
				_ = m.userInput.Session.Selector.EnterSelect(m.env.Width, m.env.Height, m.services.Session.GetStore(), m.env.CWD)
			}
		}

		m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)
		if len(cmds) > 0 {
			return tea.Batch(cmds...)
		}
		return nil
	}

	m.userInput.Textarea.SetWidth(msg.Width - 4 - 2)

	if oldWidth != msg.Width && m.conv.CommittedCount > 0 {
		return m.reflowScrollback()
	}

	return nil
}

func (m *model) reflowScrollback() tea.Cmd {
	committed := m.conv.CommittedCount
	m.conv.CommittedCount = 0

	var parts []string
	params := m.messageRenderParams()

	for i := range committed {
		if rendered := conv.RenderSingleMessage(params, i); rendered != "" {
			parts = append(parts, rendered)
		}
		m.conv.CommittedCount = i + 1
	}

	if len(parts) == 0 {
		return tea.ClearScreen
	}
	return tea.Sequence(tea.ClearScreen, tea.Println(strings.Join(parts, "\n")))
}

// ============================================================
// Submit and command execution
// ============================================================

func (m *model) handleSubmit() tea.Cmd {
	return input.HandleSubmit(m.submitDeps())
}

func (m *model) submitDeps() input.SubmitDeps {
	return input.SubmitDeps{
		Actions:         m,
		Input:           &m.userInput,
		Conversation:    &m.conv.ConversationModel,
		CheckPromptHook: m.checkPromptHook,
		Cwd:             m.env.CWD,
		HandleCommand: func(text string) (tea.Cmd, bool) {
			ctrl := input.NewCommandController(m.commandDeps())
			return ctrl.HandleSubmit(text)
		},
		ClearPluginRoot: m.services.Plugin.ClearActivePluginRoot,
	}
}

func (m *model) StartProviderTurn(content string) tea.Cmd {
	log.QueueLog("StartProviderTurn: %q", truncate(content, 60))
	if m.env.LLMProvider == nil {
		m.conv.Append(core.ChatMessage{
			Role:    core.RoleNotice,
			Content: "No provider connected. Use /provider to connect.",
		})
		return tea.Batch(m.CommitMessages()...)
	}

	startCmd, err := m.ensureAgentSession()
	if err != nil {
		m.conv.Append(core.ChatMessage{
			Role:    core.RoleNotice,
			Content: "Failed to start agent: " + err.Error(),
		})
		return tea.Batch(m.CommitMessages()...)
	}

	m.env.DetectThinkingKeywords(content)

	var images []core.Image
	if len(m.conv.Messages) > 0 {
		lastMsg := m.conv.Messages[len(m.conv.Messages)-1]
		images = lastMsg.Images
	}

	sendCmd := m.sendToAgent(content, images)
	if startCmd != nil {
		return tea.Batch(startCmd, sendCmd)
	}
	return sendCmd
}

func (m *model) commandDeps() input.CommandDeps {
	return input.CommandDeps{
		Input:        &m.userInput,
		Conversation: &m.conv.ConversationModel,
		Tool:         &m.conv.Tool,
		Width:        m.env.Width,
		Height:       m.env.Height,
		Cwd:          m.env.CWD,

		DisabledTools: m.services.Setting.DisabledTools(),
		ProviderStore: m.services.LLM.Store(),
		LLMProvider:   m.env.LLMProvider,
		InputTokens:   m.env.InputTokens,
		CurrentModel:  m.env.CurrentModel,

		Command: m.services.Command,
		Skill:   m.services.Skill,
		Plugin:  m.services.Plugin,
		MCP:     m.services.MCP,
		Tracker: m.services.Tracker,
		Cron:    m.services.Cron,
		ToolSvc: m.services.Tool,

		GetSessionID:      func() string { return m.services.Session.ID() },
		GetSessionStore:   func() *session.Store { return m.services.Session.GetStore() },
		GetThinkingEffort: func() string { return m.env.EffectiveThinkingEffort() },

		ResetTokens:        m.env.ResetTokens,
		SetThinkingEffort:  func(effort string) { m.env.ThinkingEffort = effort },
		EnsureSessionStore: func(cwd string) error { return m.services.Session.EnsureStore(cwd) },
		ForkSession:        m.forkSession,

		CommitMessages:          m.CommitMessages,
		StartProviderTurn:       m.StartProviderTurn,
		HandleSkillInvocation:   m.HandleSkillInvocation,
		StartExternalEditor:     m.StartExternalEditor,
		ReloadPluginBackedState: m.ReloadPluginBackedState,
		PersistSession:          m.PersistSession,
		InitTaskStorage:         m.InitTaskStorage,
		ReconfigureAgentTool:    m.ReconfigureAgentTool,
		StopAgentSession:        m.StopAgentSession,
		FireSessionEnd:          m.FireSessionEnd,
		BuildCompactRequest:     m.BuildCompactRequest,
		SpinnerTickCmd:          m.SpinnerTickCmd,
		ResetCronQueue:          m.ResetCronQueue,
	}
}

func (m *model) executeCommand(ctx context.Context, inputText string) (string, tea.Cmd, bool) {
	return input.NewCommandController(m.commandDeps()).Execute(ctx, inputText)
}

// ============================================================
// Approval flow
// ============================================================

func (m *model) approvalDeps() input.ApprovalFlowDeps {
	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook.Engine()
	}
	return input.ApprovalFlowDeps{
		Actions:            m,
		Input:              &m.userInput,
		HookEngine:         hookEngine,
		Settings:           m.services.Setting.Snapshot(),
		SessionPermissions: m.env.SessionPermissions,
		SetOperationMode:   func(mode setting.OperationMode) { m.env.OperationMode = mode },
		Tool:               &m.conv.Tool,
		Width:              m.env.Width,
		Height:             m.env.Height,
		Cwd:                m.env.CWD,
		ProgressHub:        m.conv.ProgressHub,
		MCPExecutor:        conv.NewMCPExecutor(m.services.MCP),
	}
}

func (m *model) AbortToolWithError(errorMsg string, retry bool) tea.Cmd {
	if m.conv.Tool.PendingCalls == nil || m.conv.Tool.CurrentIdx >= len(m.conv.Tool.PendingCalls) {
		m.conv.Tool.Reset()
		m.conv.Stream.Stop()
		return tea.Batch(m.CommitMessages()...)
	}
	tc := m.conv.Tool.PendingCalls[m.conv.Tool.CurrentIdx]
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, ToolName: tc.Name, ToolResult: &core.ToolResult{ToolCallID: tc.ID, Content: errorMsg, IsError: true}})
	m.cancelRemainingToolCalls(m.conv.Tool.CurrentIdx + 1)
	m.conv.Tool.Reset()
	m.conv.Stream.Stop()
	commitCmds := m.CommitMessages()
	if retry {
		commitCmds = append(commitCmds, m.ContinueOutbox())
	}
	return tea.Batch(commitCmds...)
}

// ============================================================
// Mode handling (operation mode, plan, question, enter-plan)
// ============================================================

func (m *model) cycleOperationMode() {
	allowBypass := m.services.Setting != nil && m.services.Setting.AllowBypass()
	m.env.OperationMode = m.env.OperationMode.NextWithBypass(allowBypass)
	m.env.ApplyModePermissions(m.env.CWD)

	if m.services.Hook != nil {
		m.services.Hook.SetPermissionMode(m.env.OperationModeName())
	}
}

func (m *model) updateMode(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case conv.ProgressQuestionMsg:
		cmd := m.handleQuestionRequest(conv.QuestionRequestMsg{
			Request: msg.Request,
			Reply:   msg.Reply,
		})
		if m.conv.ProgressHub != nil {
			cmd = tea.Batch(cmd, m.conv.ProgressHub.Check())
		}
		return cmd, true
	case conv.QuestionRequestMsg:
		return m.handleQuestionRequest(msg), true
	}
	return nil, false
}

func (m *model) handleQuestionRequest(msg conv.QuestionRequestMsg) tea.Cmd {
	m.conv.Modal.PendingQuestion = msg.Request
	m.conv.Modal.PendingQuestionReply = msg.Reply
	m.conv.Modal.Question.Show(msg.Request, m.env.Width)
	return tea.Batch(m.CommitMessages()...)
}

func (m *model) handleQuestionResponse(msg conv.QuestionResponseMsg) tea.Cmd {
	reply := m.conv.Modal.PendingQuestionReply
	m.conv.Modal.PendingQuestionReply = nil
	defer func() { m.conv.Modal.PendingQuestion = nil }()

	if reply == nil {
		return nil
	}

	if msg.Cancelled {
		reply <- &tool.QuestionResponse{
			RequestID: msg.Request.ID,
			Cancelled: true,
		}
		return nil
	}
	reply <- msg.Response
	return nil
}

// ============================================================
// Input side effects
// ============================================================

func (m *model) handleStreamCancel() tea.Cmd {
	m.services.Agent.Stop()
	m.conv.Stream.Stop()
	m.conv.ProgressHub.DrainPendingQuestions()
	m.conv.Modal.Question.Hide()
	m.cancelPendingToolCalls()
	m.conv.MarkLastInterrupted()

	cmds := m.CommitMessages()
	if cmd := input.DrainInputQueue(m.submitDeps()); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) cancelPendingToolCalls() {
	toolCalls := m.conv.Tool.DrainPendingCalls()
	if toolCalls == nil && len(m.conv.Messages) > 0 {
		lastMsg := m.conv.Messages[len(m.conv.Messages)-1]
		if lastMsg.Role == core.RoleAssistant {
			toolCalls = lastMsg.ToolCalls
		}
	}
	m.conv.AppendCancelledToolResults(toolCalls, func(tc core.ToolCall) string {
		if tc.Name == "TaskOutput" {
			return "Stopped waiting for background task output because the user sent a new message. The background task may still be running."
		}
		return "Tool execution interrupted because the user sent a new message."
	})
}

func (m *model) cancelRemainingToolCalls(startIdx int) {
	m.conv.AppendCancelledToolResults(m.conv.Tool.RemainingCalls(startIdx), func(core.ToolCall) string {
		return "Tool execution skipped."
	})
}

func (m *model) HandleSkillInvocation() tea.Cmd {
	if m.env.LLMProvider == nil {
		m.conv.AddNotice("No provider connected. Use /provider to connect.")
		m.userInput.Skill.ClearPending()
		return tea.Batch(m.CommitMessages()...)
	}

	startCmd, err := m.ensureAgentSession()
	if err != nil {
		m.conv.AddNotice("Failed to start agent: " + err.Error())
		m.userInput.Skill.ClearPending()
		return tea.Batch(m.CommitMessages()...)
	}

	displayMsg, fullMsg := m.userInput.Skill.ConsumeInvocation()
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: fullMsg, DisplayContent: displayMsg})
	sendCmd := m.sendToAgent(fullMsg, nil)
	if startCmd != nil {
		return tea.Batch(startCmd, sendCmd)
	}
	return sendCmd
}

func (m *model) pasteImageFromClipboard() (tea.Cmd, bool) {
	imgData, err := image.ReadImageToProviderData()
	if err != nil {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Image paste error: " + err.Error()})
		return tea.Batch(m.CommitMessages()...), true
	}
	if imgData == nil {
		return nil, false
	}
	label := m.userInput.AddPendingImage(*imgData)
	m.userInput.Images.Selection = input.ImageSelection{}
	m.userInput.Textarea.InsertString(label)
	m.userInput.UpdateHeight()
	return nil, true
}

func (m *model) QuitWithCancel() (tea.Cmd, bool) {
	m.services.Agent.Stop()
	m.conv.Stream.Stop()
	if m.conv.Tool.Cancel != nil {
		m.conv.Tool.Cancel()
	}
	m.FireSessionEnd("prompt_input_exit")
	return tea.Quit, true
}

// ============================================================
// Permission bridge response
// ============================================================

type permissionDecision struct {
	Approved bool
	AllowAll bool
	Request  *perm.PermissionRequest
}

func (m *model) handlePermBridgeDecision(decision permissionDecision) tea.Cmd {
	if !m.services.Agent.Active() {
		return nil
	}
	req := m.services.Agent.PendingPermission()
	m.services.Agent.SetPendingPermission(nil)
	if req == nil {
		return nil
	}
	resp := conv.PermBridgeResponse{Allow: decision.Approved, Reason: "user decision"}
	if decision.Approved {
		if decision.AllowAll && m.env.SessionPermissions != nil && decision.Request != nil {
			m.env.SessionPermissions.AllowTool(decision.Request.ToolName)
		}
		resp.Reason = "user approved"
	} else {
		resp.Reason = "user denied"
	}
	select {
	case req.Response <- resp:
	default:
	}
	return conv.PollPermBridge(m.services.Agent.PermissionBridge())
}
