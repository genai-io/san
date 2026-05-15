// Root model: struct definition, construction, message pipeline, session
// persistence, conv.Runtime event handlers, deps builders, and internal helpers.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/app/conv"
	"github.com/genai-io/gen-code/internal/app/hub"
	"github.com/genai-io/gen-code/internal/app/input"
	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/app/trigger"
	"github.com/genai-io/gen-code/internal/command"
	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/core/system"
	"github.com/genai-io/gen-code/internal/filecache"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/identity"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/llm/deepseek"
	"github.com/genai-io/gen-code/internal/llm/minmax"
	"github.com/genai-io/gen-code/internal/log"
	"github.com/genai-io/gen-code/internal/mcp"
	"github.com/genai-io/gen-code/internal/plugin"
	"github.com/genai-io/gen-code/internal/session"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/skill"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/task"
	"github.com/genai-io/gen-code/internal/task/tracker"
	"github.com/genai-io/gen-code/internal/tool"
)

const defaultWidth = 80

// ============================================================
// Model struct
// ============================================================

type model struct {
	// ── Sub-models (one per event source / concern) ─────────────
	userInput   input.Model    // Source 1: user keyboard input
	eventHub    *hub.Hub       // Source 2: inter-agent event routing (pure pub/sub)
	mainEvents  chan hub.Event // TUI turn-boundary buffer: batches async events (task completions, agent messages) for priority-ordered drain
	systemInput trigger.Model  // Source 3: system events (cron/hooks/watcher)
	conv        conv.Model     // Agent Outbox: conversation + output rendering
	env         env            // Shared app state: provider, session, permission, plan, config
	services    services       // Domain service singletons, injected at construction
}

var (
	_ conv.Runtime          = (*model)(nil)
	_ input.SubmitRuntime   = (*model)(nil)
	_ input.ApprovalRuntime = (*model)(nil)
)

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.userInput.MCP.Selector.AutoConnect(),
		trigger.TriggerCronTickNow(),
		trigger.StartCronTicker(),
		trigger.StartAsyncHookTicker(),
	}
	if m.env.InitialPrompt != "" {
		prompt := m.env.InitialPrompt
		cmds = append(cmds, func() tea.Msg { return initialPromptMsg(prompt) })
	}
	return tea.Batch(cmds...)
}

// ============================================================
// Model construction
// ============================================================

func newModel(opts setting.RunOptions) (*model, error) {
	base := newBaseModel()
	m := &base

	m.eventHub.Register("main", func(e hub.Event) { m.mainEvents <- e })

	// Wire task completion: closure captures hub + hooks + tracker directly.
	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook.Engine()
	}
	m.wireTaskLifecycle(hookEngine)

	m.configureAsyncHookCallback()
	m.ensureMemoryContextLoaded()
	m.ReconfigureAgentTool()
	m.wireReminderProviders()
	m.InitTaskStorage()
	if err := m.applyRunOptions(opts); err != nil {
		return nil, err
	}
	return m, nil
}

func newBaseModel() model {
	svc := newServices()
	environment := newEnv(svc.LLM, appCwd, svc.Setting.IsGitRepo(appCwd))
	if settings := svc.Setting.Snapshot(); settings != nil {
		environment.ApplyDefaultPermissionMode(settings.Permissions.DefaultMode, appCwd, svc.Setting.AllowBypass())
	}
	return model{
		userInput: input.New(appCwd, defaultWidth, commandSuggestionMatcher(svc.Command), input.SelectorDeps{
			AgentRegistry:    &agentRegistryAdapter{svc.Subagent.Registry()},
			SkillRegistry:    svc.Skill.Registry(),
			MCPRegistry:      svc.MCP.Registry(),
			PluginRegistry:   svc.Plugin.Registry(),
			IdentityRegistry: svc.Identity,
			Setting:          svc.Setting,
			LoadDisabled:     svc.Setting.GetDisabledToolsAt,
			UpdateDisabled:   svc.Setting.UpdateDisabledToolsAt,
		}),
		conv:        conv.NewModel(defaultWidth),
		eventHub:    hub.New(),
		mainEvents:  make(chan hub.Event, 64),
		systemInput: trigger.New(),
		env:         environment,
		services:    svc,
	}
}

func (m *model) applyRunOptions(opts setting.RunOptions) error {
	if opts.PluginDir != "" {
		ctx := context.Background()
		if err := m.services.Plugin.LoadFromPath(ctx, opts.PluginDir); err != nil {
			return fmt.Errorf("failed to load plugins from %s: %w", opts.PluginDir, err)
		}
		if err := m.ReloadPluginBackedState(); err != nil {
			return err
		}
	}

	if opts.Prompt != "" {
		m.env.InitialPrompt = opts.Prompt
	}

	if opts.Continue {
		if err := m.applyContinueOption(); err != nil {
			return err
		}
	}

	if opts.Resume {
		if err := m.applyResumeOption(opts.ResumeID); err != nil {
			return err
		}
	}

	return nil
}

func (m *model) ReloadPluginBackedState() error {
	skill.Initialize(skill.Options{CWD: m.env.CWD})
	command.Initialize(command.Options{
		CWD:                m.env.CWD,
		DynamicProviders:   []func() []command.Info{skillCommandInfos},
		PluginCommandPaths: pluginCommandPaths,
	})
	subagent.Initialize(subagent.Options{CWD: m.env.CWD, PluginAgentPaths: pluginAgentPaths})
	mcp.Initialize(mcp.Options{CWD: m.env.CWD, PluginServers: pluginMCPServers})
	setting.Initialize(setting.Options{CWD: m.env.CWD})

	m.services.refreshAfterReload()
	m.userInput.Identity.SetRegistry(m.services.Identity)

	if m.services.Hook != nil {
		plugin.MergePluginHooksIntoSettings(m.services.Setting.Snapshot())
	}
	m.syncSettingsToHookEngine()
	m.ReconfigureAgentTool()

	// Refresh skills/memory reminders so the LLM sees the updated skill set
	// in the next user message instead of waiting for SessionStart/PostCompact.
	if m.services.Reminder != nil {
		m.services.Reminder.EnqueueAllProviders()
	}

	return nil
}

func (m *model) applyContinueOption() error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	sess, err := m.services.Session.LoadLatest()
	if err != nil {
		return fmt.Errorf("no previous session to continue: %w", err)
	}

	m.restoreSessionData(sess)
	return nil
}

func (m *model) applyResumeOption(resumeID string) error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return fmt.Errorf("failed to initialize session store: %w", err)
	}

	if resumeID != "" {
		sess, err := m.services.Session.Load(resumeID)
		if err != nil {
			return fmt.Errorf("failed to load session %s: %w", resumeID, err)
		}
		m.restoreSessionData(sess)
		return nil
	}

	m.userInput.Session.PendingSelector = true
	return nil
}

func (m *model) BuildCompactRequest(focus, trigger string) conv.CompactRequest {
	var hookEngine *hook.Engine
	if m.services.Hook != nil {
		hookEngine = m.services.Hook.Engine()
	}
	return conv.CompactRequest{
		Ctx:        context.Background(),
		Client:     m.buildLLMClient(),
		Messages:   m.conv.ConvertToProvider(),
		Focus:      focus,
		HookEngine: hookEngine,
		Trigger:    trigger,
	}
}

func (m *model) ensureMemoryContextLoaded() {
	if m.env.CachedUserInstructions != "" || m.env.CachedProjectInstructions != "" {
		return
	}
	m.refreshMemoryContext(m.env.CWD, "session_start")
}

// ============================================================
// Message commit pipeline
// ============================================================

func (m *model) CommitMessages() []tea.Cmd {
	return m.renderAndCommit(true)
}

func (m *model) commitAllMessages() []tea.Cmd {
	return m.renderAndCommit(false)
}

func (m *model) renderAndCommit(checkReady bool) []tea.Cmd {
	var parts []string
	lastIdx := len(m.conv.Messages) - 1
	params := m.messageRenderParams()

	for i := m.conv.CommittedCount; i < len(m.conv.Messages); i++ {
		msg := m.conv.Messages[i]

		if checkReady {
			if i == lastIdx && msg.Role == core.RoleAssistant && m.conv.Stream.Active {
				break
			}
		}

		if rendered := conv.RenderSingleMessage(params, i); rendered != "" {
			parts = append(parts, rendered)
		}
		m.conv.CommittedCount = i + 1
	}

	if len(parts) == 0 {
		return nil
	}
	return []tea.Cmd{tea.Println(strings.Join(parts, "\n"))}
}

// ============================================================
// Session persistence
// ============================================================

func (m *model) InitTaskStorage() {
	m.initTaskStorage(m.services.Session.ID())
}

func (m *model) PersistSession() error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}
	if len(m.conv.Messages) == 0 {
		return nil
	}

	entries := session.ConvertToEntries(m.conv.Messages)

	var providerName, modelID string
	if m.env.CurrentModel != nil {
		providerName = string(m.env.CurrentModel.Provider)
		modelID = m.env.CurrentModel.ModelID
	}

	sess := &session.Snapshot{
		Metadata: session.SessionMetadata{
			ID:         m.services.Session.ID(),
			Provider:   providerName,
			Model:      modelID,
			Cwd:        m.env.CWD,
			LastPrompt: session.ExtractLastUserText(entries),
			Mode:       m.env.SessionMode(),
		},
		Entries: entries,
		Tasks:   m.services.Tracker.Export(),
	}

	if sess.Metadata.Title == "" || sess.Metadata.ID == "" {
		sess.Metadata.Title = session.GenerateTitle(sess.Entries)
	}

	if err := m.services.Session.Save(sess); err != nil {
		return err
	}

	m.services.Session.SetID(sess.Metadata.ID)
	m.initTaskStorage(m.services.Session.ID())

	if m.services.Hook != nil {
		m.services.Hook.SetTranscriptPath(m.services.Session.GetStore().SessionPath(sess.Metadata.ID))
	}
	m.ReconfigureAgentTool()

	return nil
}

type persistSessionDoneMsg struct{ err error }

// Only safe when the session ID is already established (i.e. not the first save).
func (m *model) persistSessionCmd() tea.Cmd {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		log.Logger().Warn("failed to ensure session store for async persist", zap.Error(err))
		return nil
	}
	if len(m.conv.Messages) == 0 {
		return nil
	}

	entries := session.ConvertToEntries(m.conv.Messages)

	var providerName, modelID string
	if m.env.CurrentModel != nil {
		providerName = string(m.env.CurrentModel.Provider)
		modelID = m.env.CurrentModel.ModelID
	}

	sess := &session.Snapshot{
		Metadata: session.SessionMetadata{
			ID:         m.services.Session.ID(),
			Provider:   providerName,
			Model:      modelID,
			Cwd:        m.env.CWD,
			LastPrompt: session.ExtractLastUserText(entries),
			Mode:       m.env.SessionMode(),
		},
		Entries: entries,
		Tasks:   m.services.Tracker.Export(),
	}

	if sess.Metadata.Title == "" {
		sess.Metadata.Title = session.GenerateTitle(sess.Entries)
	}

	store := m.services.Session.GetStore()
	return func() tea.Msg {
		if store == nil {
			return persistSessionDoneMsg{err: fmt.Errorf("no session store")}
		}
		return persistSessionDoneMsg{err: store.Save(sess)}
	}
}

func (m *model) loadSessionByID(id string) error {
	if err := m.services.Session.EnsureStore(m.env.CWD); err != nil {
		return err
	}

	sess, err := m.services.Session.Load(id)
	if err != nil {
		return err
	}

	m.services.Tracker.SetStorageDir("")
	m.restoreSessionData(sess)

	if len(sess.Tasks) == 0 {
		m.services.Tracker.Reset()
	}

	m.env.InputTokens = 0
	m.env.OutputTokens = 0

	return nil
}

func (m *model) restoreSessionData(sess *session.Snapshot) {
	m.conv.Messages = session.ConvertFromEntries(sess.Entries)
	m.services.Session.SetID(sess.Metadata.ID)

	m.initTaskStorage(m.services.Session.ID())

	if len(sess.Tasks) > 0 {
		m.services.Tracker.Import(sess.Tasks)
	}
}

func (m *model) initTaskStorage(sessionID string) {
	if m.services.Tracker.GetStorageDir() != "" {
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Logger().Warn("failed to get home directory for task storage", zap.Error(err))
		return
	}

	taskListID := os.Getenv("GEN_TASK_LIST_ID")
	if taskListID != "" {
		dir := filepath.Join(homeDir, ".gen", "tasks", taskListID)
		m.services.Tracker.SetStorageDir(dir)
		_ = m.services.Task.SetOutputDir(filepath.Join(dir, "outputs"))
		return
	}

	if sessionID == "" {
		return
	}
	dir := filepath.Join(homeDir, ".gen", "tasks", sessionID)
	m.services.Tracker.SetStorageDir(dir)
	_ = m.services.Task.SetOutputDir(filepath.Join(dir, "outputs"))
}

// ============================================================
// conv.Runtime — agent outbox event handlers
// ============================================================

func (m *model) BeginInferTurn() {
	if m.env.turnUsageActive {
		return
	}
	m.env.TurnInputTokens = 0
	m.env.TurnOutputTokens = 0
	m.env.turnUsageActive = true
}

func (m *model) SetTokenUsage(resp *core.InferResponse) {
	if resp == nil {
		return
	}

	if m.userInput.Provider.StatusMessage == "compacted" {
		m.userInput.Provider.StatusMessage = ""
	}

	// Bottom-right context usage reflects the latest prompt/output, not a
	// lifetime sum across the whole session.
	m.env.InputTokens = resp.TokensIn
	m.env.OutputTokens = resp.TokensOut
	m.env.TurnInputTokens += resp.TokensIn
	m.env.TurnOutputTokens += resp.TokensOut

	if m.env.CurrentModel != nil {
		switch m.env.CurrentModel.Provider {
		case llm.MinMax:
			cost, ok := minmax.EstimateCost(m.env.CurrentModel.ModelID, llm.Usage{
				InputTokens:              resp.TokensIn,
				OutputTokens:             resp.TokensOut,
				CacheCreationInputTokens: resp.CacheCreateTokens,
				CacheReadInputTokens:     resp.CacheReadTokens,
			})
			if ok {
				m.env.ConversationCost = m.env.ConversationCost.Add(cost)
			}
		case llm.DeepSeek:
			cost, ok := deepseek.EstimateCost(m.env.CurrentModel.ModelID, llm.Usage{
				InputTokens:              resp.TokensIn,
				OutputTokens:             resp.TokensOut,
				CacheCreationInputTokens: resp.CacheCreateTokens,
				CacheReadInputTokens:     resp.CacheReadTokens,
			})
			if ok {
				m.env.ConversationCost = m.env.ConversationCost.Add(cost)
			}
		}
	}
}

func (m *model) HasRunningTasks() bool { return m.services.Tracker.HasInProgress() }

// HandleAgentMessage observes the agent's MessageEvent echoes. Every path
// that hands a user message to the agent appends to m.conv at the call site,
// so the echo has nothing to do here — appending again would double-display.
func (m *model) HandleAgentMessage(core.Message) tea.Cmd {
	return nil
}

func (m *model) ProcessToolResult(tr core.ToolResult) *core.ToolResult {
	sideEffect := m.services.Tool.PopSideEffect(tr.ToolCallID)
	if sideEffect != nil {
		m.applyToolSideEffects(tr.ToolName, sideEffect)
	}
	m.firePostToolHook(tr, sideEffect)

	result := &core.ToolResult{
		ToolCallID: tr.ToolCallID,
		ToolName:   tr.ToolName,
		Content:    tr.Content,
		IsError:    tr.IsError,
	}
	m.persistOverflow(result)
	return result
}

func (m *model) ProcessTurnEnd(result core.Result) tea.Cmd {
	m.env.turnUsageActive = false
	if m.services.Tracker.AllDone() {
		m.services.Tracker.Reset()
	}
	log.QueueLog("ProcessTurnEnd: starting queueLen=%d", m.userInput.Queue.Len())
	commitCmds := m.CommitMessages()

	if cmd, found := m.drainTurnQueues(); found {
		log.QueueLog("ProcessTurnEnd: drained queued message, skipping hooks")
		if cmd != nil {
			commitCmds = append(commitCmds, cmd)
		}
		commitCmds = append(commitCmds, m.ContinueOutbox())
		return tea.Batch(commitCmds...)
	}

	log.QueueLog("ProcessTurnEnd: firing idle hooks async")
	commitCmds = append(commitCmds, m.fireIdleHooksCmd(result), m.ContinueOutbox())
	return tea.Batch(commitCmds...)
}

func (m *model) ProcessAgentStop(err error) tea.Cmd {
	m.env.turnUsageActive = false
	// /clear and manual stop cancel the active agent context; that is expected
	// shutdown, not an agent failure the user needs to see.
	if err != nil && !errors.Is(err, context.Canceled) {
		m.conv.AddNotice(fmt.Sprintf("Agent error: %v", err))
		m.fireStopFailureHook(core.LastAssistantChatContent(m.conv.Messages), err)
	}
	m.conv.ProgressHub.DrainPendingQuestions()
	m.conv.Modal.Question.Hide()
	commitCmds := m.CommitMessages()
	m.StopAgentSession()
	return tea.Batch(commitCmds...)
}

func (m *model) HandleAgentCompact(info core.CompactInfo) tea.Cmd {
	scrollbackCmds := m.commitAllMessages()
	boundaryStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	boundary := boundaryStyle.Render(fmt.Sprintf("✻ Conversation compacted — %d messages summarized (scroll up for history)", info.OriginalCount))

	m.conv.Clear()
	m.env.ResetContextDisplay()
	token := m.userInput.Provider.SetStatusMessage("compacted")
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: core.FormatCompactSummary(info.Summary)})

	if m.services.Hook != nil {
		m.services.Hook.ExecuteAsync(hook.PostCompact, hook.HookInput{Trigger: "auto"})
	}
	// Auto-compact summarizes the conversation history, dropping any
	// system-reminder content that previously rode on user messages. Re-enqueue
	// session-level reminders so they reattach to the next user turn and the
	// LLM still has skills/memory/etc. context.
	if m.services.Reminder != nil {
		m.services.Reminder.EnqueueAllProviders()
	}

	scrollPart := tea.Sequence(append(scrollbackCmds, tea.Println(boundary), tea.ClearScreen)...)
	return tea.Batch(scrollPart, m.ContinueOutbox(), kit.StatusTimer(3*time.Second, token))
}

// HandleCompactResult handles manual /compact results.
// Stops the agent so the next user message restarts it with compacted messages.
func (m *model) HandleCompactResult(msg conv.CompactResultMsg) tea.Cmd {
	if msg.Error != nil {
		m.conv.Compact.Complete(fmt.Sprintf("Compaction could not be completed: %v", msg.Error), true)
		return tea.Batch(m.CommitMessages()...)
	}
	m.conv.Compact.Complete(fmt.Sprintf("Condensed %d earlier messages.", msg.OriginalCount), false)
	scrollbackCmds := m.commitAllMessages()
	boundaryStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	boundary := boundaryStyle.Render(fmt.Sprintf("✻ Conversation compacted — %d messages summarized (scroll up for history)", msg.OriginalCount))

	m.conv.Clear()
	m.env.ResetTokens()
	token := m.userInput.Provider.SetStatusMessage("compacted")
	m.StopAgentSession()

	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: core.FormatCompactSummary(msg.Summary)})

	var restoredFiles []filecache.RestoredFile
	if m.env.FileCache != nil {
		restoredFiles, _ = m.env.FileCache.RestoreRecent()
		if len(restoredFiles) > 0 {
			restoredContext := filecache.FormatRestoredFiles(restoredFiles)
			m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: restoredContext})
			m.conv.AddNotice(fmt.Sprintf("Restored %d recently accessed file(s) for context.", len(restoredFiles)))
		}
	}
	if m.services.Hook != nil {
		m.services.Hook.ExecuteAsync(hook.PostCompact, hook.HookInput{Trigger: msg.Trigger})
	}
	// Manual /compact also drops conversation-history reminders. Re-enqueue
	// providers so the next user turn carries fresh session-level context.
	if m.services.Reminder != nil {
		m.services.Reminder.EnqueueAllProviders()
	}

	scrollPart := tea.Sequence(append(scrollbackCmds, tea.Println(boundary), tea.ClearScreen)...)
	return tea.Batch(scrollPart, tea.Batch(m.CommitMessages()...), kit.StatusTimer(3*time.Second, token))
}

func (m *model) HandleTokenLimitResult(msg kit.TokenLimitResultMsg) tea.Cmd {
	m.userInput.Provider.FetchingLimits = false
	var content string
	if msg.Error != nil {
		content = "Error: " + msg.Error.Error()
	} else {
		content = msg.Result
	}
	m.conv.AddNotice(content)
	return tea.Batch(m.CommitMessages()...)
}

// ============================================================
// Internal: tool side effects and context changes
// ============================================================

func (m *model) applyToolSideEffects(toolName string, sideEffect any) {
	resp, ok := sideEffect.(map[string]any)
	if !ok {
		return
	}
	m.trackAgentLaunch(toolName, resp)
	switch toolName {
	case "Bash":
		if newCwd := kit.MapString(resp, "cwd"); newCwd != "" {
			m.changeCwd(newCwd)
		}
	case tool.ToolEnterWorktree:
		if worktreePath := kit.MapString(resp, "worktreePath"); worktreePath != "" {
			m.changeCwd(worktreePath)
		}
	case tool.ToolExitWorktree:
		if restoredPath := kit.MapString(resp, "restoredPath"); restoredPath != "" {
			m.changeCwd(restoredPath)
		}
	case "Write", "Edit":
		if filePath := kit.MapString(resp, "filePath"); filePath != "" {
			m.fireFileChanged(filePath, toolName)
			m.reloadIdentitiesIfChanged(filePath)
			if m.env.FileCache != nil {
				m.env.FileCache.Touch(filePath)
			}
		}
	case "Read":
		if fileData, ok := resp["file"].(map[string]any); ok {
			if filePath := kit.MapString(fileData, "filePath"); filePath != "" && m.env.FileCache != nil {
				m.env.FileCache.Touch(filePath)
			}
		}
	}
}

func (m *model) trackAgentLaunch(toolName string, resp map[string]any) {
	if !tool.IsAgentToolName(toolName) {
		return
	}
	bg, ok := resp["backgroundTask"].(map[string]any)
	if !ok {
		return
	}
	launch := tracker.BackgroundTaskLaunch{
		TaskID:      kit.MapString(bg, "taskId"),
		AgentName:   kit.MapString(bg, "agentName"),
		AgentType:   kit.MapString(bg, "agentType"),
		Description: kit.MapString(bg, "description"),
	}
	if launch.TaskID == "" {
		return
	}
	tracker.TrackWorker(m.services.Tracker, launch)
}

func (m *model) persistOverflow(result *core.ToolResult) {
	const overflowThreshold = 100_000
	const previewSize = 10_000

	if len(result.Content) <= overflowThreshold {
		return
	}
	cutoff := min(previewSize, len(result.Content))
	for cutoff > 0 && !utf8.RuneStart(result.Content[cutoff]) {
		cutoff--
	}
	preview := result.Content[:cutoff]
	persisted := false
	if err := m.services.Session.EnsureStore(m.env.CWD); err == nil && m.services.Session.ID() != "" {
		if err := m.services.Session.GetStore().PersistToolResult(m.services.Session.ID(), result.ToolCallID, result.Content); err == nil {
			persisted = true
		}
	}
	if persisted {
		result.Content = fmt.Sprintf("%s\n\n[Full output persisted to blobs/tool-result/%s/%s]", preview, m.services.Session.ID(), result.ToolCallID)
	} else {
		result.Content = fmt.Sprintf("%s\n\n[Output truncated from %d bytes — full content not persisted]", preview, len(result.Content))
	}
}

func (m *model) changeCwd(newCwd string) {
	if newCwd == "" || newCwd == m.env.CWD {
		return
	}
	oldCwd := m.env.CWD
	m.env.CWD = newCwd
	m.env.IsGit = m.services.Setting.IsGitRepo(newCwd)
	m.userInput.HandleCwdChange(newCwd)
	m.env.ClearCachedInstructions()
	m.refreshMemoryContext(newCwd, "cwd_changed")
	m.ReloadProjectContext(newCwd)
	m.ReconfigureAgentTool()
	if m.services.Hook != nil {
		m.services.Hook.SetCwd(newCwd)
		m.services.Hook.ExecuteAsync(hook.CwdChanged, hook.HookInput{OldCwd: oldCwd, NewCwd: newCwd})
	}
}

func (m *model) fireFileChanged(filePath, source string) {
	if m.services.Hook == nil || filePath == "" {
		return
	}
	m.services.Hook.ExecuteAsync(hook.FileChanged, hook.HookInput{FilePath: filePath, Source: source, Event: "change"})
}

func (m *model) ReloadProjectContext(cwd string) {
	initExtensions(cwd)
	setting.Initialize(setting.Options{CWD: cwd})
	m.services.refreshAfterReload()
	m.userInput.Identity.SetRegistry(m.services.Identity)
	if m.services.Hook != nil {
		plugin.MergePluginHooksIntoSettings(m.services.Setting.Snapshot())
	}
	m.syncSettingsToHookEngine()
}

func (m *model) reloadIdentitiesIfChanged(filePath string) {
	if !identity.IsIdentityFile(m.env.CWD, filePath) || m.services.Identity == nil {
		return
	}
	m.services.Identity.Reload()
	m.userInput.Identity.SetRegistry(m.services.Identity)
	m.ReconfigureAgentTool()
}

func (m *model) applyStartupHookOutcome(outcome hook.HookOutcome) {
	if outcome.InitialUserMessage != "" && m.env.InitialPrompt == "" && len(m.conv.Messages) == 0 {
		m.env.InitialPrompt = outcome.InitialUserMessage
	}
	if len(outcome.WatchPaths) == 0 {
		return
	}
	if m.systemInput.FileWatcher == nil {
		m.systemInput.FileWatcher = trigger.NewFileWatcher(m.services.Hook.Engine(), func(outcome hook.HookOutcome) {
			if m.systemInput.AsyncHookQueue != nil && outcome.InitialUserMessage != "" {
				m.systemInput.AsyncHookQueue.Push(trigger.AsyncHookRewake{Notice: "File watcher hook triggered", Context: []string{outcome.InitialUserMessage}})
			}
		})
	}
	m.systemInput.FileWatcher.SetPaths(outcome.WatchPaths)
}

// ============================================================
// Internal: turn lifecycle and queue drain
// ============================================================

func (m *model) handleStopHookResult(msg stopHookResultMsg) tea.Cmd {
	if msg.Blocked {
		log.QueueLog("handleStopHookResult: hooks BLOCKED reason=%q", msg.Reason)
		blockMsg := "Stop hook blocked: " + msg.Reason
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: blockMsg})
		return m.sendToAgent(blockMsg, nil)
	}
	log.QueueLog("handleStopHookResult: hooks done, persisting")
	var cmds []tea.Cmd
	if m.services.Session.ID() != "" {
		cmds = append(cmds, m.persistSessionCmd())
	} else {
		if err := m.PersistSession(); err != nil {
			log.Logger().Warn("failed to save session", zap.Error(err))
		}
	}
	if cmd := input.StartPromptSuggestion(m.promptSuggestionDeps()); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if msg.Result.StopReason != "" && msg.Result.StopReason != core.StopEndTurn {
		m.conv.AddNotice(fmt.Sprintf("Agent stopped: %s", msg.Result.StopReason))
		if msg.Result.StopDetail != "" {
			m.conv.AddNotice(msg.Result.StopDetail)
		}
	}
	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func (m *model) drainTurnQueues() (tea.Cmd, bool) {
	// Drain ONE user message per call so each gets its own agent response.
	// The agent's inner loop also drains one inbox message at a time,
	// producing one TurnEvent per queued message.
	if item, ok := m.userInput.Queue.Dequeue(); ok {
		log.QueueLog("drainTurnQueues: dequeued %q remaining=%d", truncate(item.Content, 60), m.userInput.Queue.Len())
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: item.Content, Images: item.Images})
		m.services.Agent.Send(item.Content, item.Images)
		return nil, true
	}

	if len(m.systemInput.CronQueue) > 0 {
		prompt := m.systemInput.CronQueue[0]
		m.systemInput.CronQueue = m.systemInput.CronQueue[1:]
		return m.injectCronPrompt(prompt), true
	}

	if m.systemInput.AsyncHookQueue != nil {
		if item, ok := m.systemInput.AsyncHookQueue.Pop(); ok {
			return m.injectAsyncHookContinuation(item), true
		}
	}

	if events := drainEvents(m.mainEvents, maxEventsPerDrain); len(events) > 0 {
		msgs := eventsToMessages(events)
		return m.injectNotification(hub.Merge(msgs)), true
	}

	return nil, false
}

func (m *model) injectNotification(msg hub.Message) tea.Cmd {
	if msg.Notice != "" {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: msg.Notice})
	}
	if m.env.LLMProvider == nil {
		if msg.Notice == "" {
			m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "A background task completed, but no provider is connected."})
		}
		return tea.Batch(m.CommitMessages()...)
	}
	if msg.Content == "" {
		return tea.Batch(m.CommitMessages()...)
	}
	return m.sendToAgent(msg.Content, nil)
}

func (m *model) wireTaskLifecycle(hookEngine *hook.Engine) {
	trackerSvc := m.services.Tracker
	eventHub := m.eventHub

	fireHook := func(event hook.EventType, info task.TaskInfo) {
		if hookEngine == nil {
			return
		}
		subject := hub.TaskSubject(info)
		hookEngine.ExecuteAsync(event, hook.HookInput{
			TaskID:          info.ID,
			TaskSubject:     subject,
			TaskDescription: info.Description,
		})
	}

	task.SetLifecycleHandler(taskLifecycleFunc{
		onCreated: func(info task.TaskInfo) {
			fireHook(hook.TaskCreated, info)
		},
		onCompleted: func(info task.TaskInfo) {
			fireHook(hook.TaskCompleted, info)
			tracker.CompleteWorker(trackerSvc, info)

			subject := hub.TaskSubject(info)
			msg, ok := hub.TaskMessage(info, subject)
			if !ok {
				return
			}
			eventHub.Publish(hub.Event{
				Type:    "task.completed",
				Source:  fmt.Sprintf("agent:%s", info.ID),
				Target:  "main",
				Subject: msg.Notice,
				Data:    msg.Content,
			})
		},
	})
}

type taskLifecycleFunc struct {
	onCreated   func(task.TaskInfo)
	onCompleted func(task.TaskInfo)
}

func (f taskLifecycleFunc) TaskCreated(info task.TaskInfo)   { f.onCreated(info) }
func (f taskLifecycleFunc) TaskCompleted(info task.TaskInfo) { f.onCompleted(info) }

const maxEventsPerDrain = 8

func drainEvents(ch <-chan hub.Event, max int) []hub.Event {
	var out []hub.Event
	for range max {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
	return out
}

func eventsToMessages(events []hub.Event) []hub.Message {
	msgs := make([]hub.Message, len(events))
	for i, e := range events {
		msgs[i] = hub.Message{Notice: e.Subject, Content: e.Data}
	}
	return msgs
}

func (m *model) injectCronPrompt(prompt string) tea.Cmd {
	if m.env.LLMProvider == nil {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: fmt.Sprintf("Cron fired but no provider connected: %s", prompt)})
		return tea.Batch(m.CommitMessages()...)
	}
	m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Scheduled task fired"})
	m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: prompt})
	return m.sendToAgent(prompt, nil)
}

func (m *model) injectAsyncHookContinuation(item trigger.AsyncHookRewake) tea.Cmd {
	if item.Notice != "" {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: item.Notice})
	}
	if len(item.Context) == 0 {
		return tea.Batch(m.CommitMessages()...)
	}
	if m.env.LLMProvider == nil {
		m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Async hook requested a follow-up, but no provider is connected."})
		return tea.Batch(m.CommitMessages()...)
	}
	for _, ctx := range item.Context {
		m.conv.Append(core.ChatMessage{Role: core.RoleUser, Content: ctx})
	}
	return m.sendToAgent(item.ContinuationPrompt, nil)
}

// ============================================================
// Deps builders and interface adapters
// ============================================================

func (m *model) promptSuggestionDeps() input.PromptSuggestionDeps {
	return input.PromptSuggestionDeps{
		Input:        &m.userInput,
		Conversation: &m.conv.ConversationModel,
		HasProvider:  m.env.LLMProvider != nil,
		BuildClient:  m.buildLLMClient,
	}
}

// setActiveIdentity persists the user's identity choice and applies it
// without restarting the session: the running main agent's identity slot
// is hot-patched in place (visible on its next inference), and the
// subagent executor is rebuilt so future Agent tool calls inherit the new
// persona. Empty name = revert to built-in default.
func (m *model) setActiveIdentity(name string) error {
	if m.services.Setting != nil {
		if snap := m.services.Setting.Snapshot(); snap != nil && snap.Identity == name {
			return nil
		}
	}
	if err := setting.SaveIdentity(name); err != nil {
		return err
	}
	if m.services.Setting != nil {
		_ = m.services.Setting.Reload(m.env.CWD)
	}
	if m.services.Agent != nil {
		if sys := m.services.Agent.System(); sys != nil {
			system.SwapIdentity(sys, m.activeIdentityBody())
		}
	}
	m.ReconfigureAgentTool()
	return nil
}

// dispatchSlashCommand runs an arbitrary slash command as if the user had
// typed it. Used by selector hotkeys (Shift+N / Shift+E in /identity).
func (m *model) dispatchSlashCommand(cmd string) tea.Cmd {
	ctrl := input.NewCommandController(m.commandDeps())
	teaCmd, _ := ctrl.HandleSubmit(cmd)
	return teaCmd
}

func (m *model) overlayDeps() input.OverlayDeps {
	return input.OverlayDeps{
		State:             &m.userInput,
		Conv:              &m.conv.ConversationModel,
		Cwd:               m.env.CWD,
		CommitMessages:    m.CommitMessages,
		CommitAllMessages: m.commitAllMessages,
		SwitchProvider: func(p llm.Provider) {
			m.StopAgentSession()
			m.switchProvider(p)
			m.ReconfigureAgentTool()
		},
		SetCurrentModel: func(info *llm.CurrentModelInfo) {
			m.env.CurrentModel = info
		},
		ClearCachedInstructions: m.env.ClearCachedInstructions,
		RefreshMemoryContext:    m.refreshMemoryContext,
		FireFileChanged:         m.fireFileChanged,
		ReloadPluginState:       m.ReloadPluginBackedState,
		LoadSession:             m.loadSessionByID,
		SetActiveIdentity:       m.setActiveIdentity,
		DispatchSlashCommand:    m.dispatchSlashCommand,
	}
}

func (m *model) triggerDeps() trigger.Deps {
	return trigger.Deps{
		StreamActive: m.conv.Stream.Active,
		Cron:         m.services.Cron,
		InjectCron:   m.injectCronPrompt,
		InjectHook:   m.injectAsyncHookContinuation,
		AppendNotice: func(text string) {
			if text != "" {
				m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: text})
			}
		},
	}
}

func (m *model) StartExternalEditor(path string) tea.Cmd {
	return kit.StartExternalEditor(path, func(err error) tea.Msg {
		return input.MemoryEditorFinishedMsg{Err: err}
	})
}

func (m *model) SpinnerTickCmd() tea.Cmd { return m.conv.Spinner.Tick }
func (m *model) ResetCronQueue()         { m.systemInput.CronQueue = nil }

func (m *model) forkSession() (string, error) {
	if m.services.Session.ID() == "" {
		return "", fmt.Errorf("no active session to fork")
	}
	forked, err := m.services.Session.Fork(m.services.Session.ID())
	if err != nil {
		return "", err
	}
	originalID := forked.Metadata.ParentSessionID
	m.services.Session.SetID(forked.Metadata.ID)
	m.services.Tracker.SetStorageDir("")
	return originalID, nil
}

func (m *model) FireSessionEnd(reason string) {
	if m.services.Hook != nil {
		m.services.Hook.Execute(context.Background(), hook.SessionEnd, hook.HookInput{
			Reason: reason,
		})
		m.services.Hook.ClearSessionHooks()
	}
	if m.systemInput.FileWatcher != nil {
		m.systemInput.FileWatcher.Stop()
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
