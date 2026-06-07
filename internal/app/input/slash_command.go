package input

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/cron"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/task/tracker"
	"github.com/genai-io/san/internal/tool"
)

// NoProviderMsg is the canonical "no LLM provider" notice used by any
// input path that needs to bail before reaching the agent.
const NoProviderMsg = "No provider connected. Use /model to connect."

type slashCommandHandler func(*SlashCommandController, context.Context, string) (string, tea.Cmd, error)

type SlashCommandEnv struct {
	// UI state: the textarea, conversation render state, tool exec state,
	// terminal dimensions, current working directory, and token/cost
	// snapshots for display.
	Input            *Model
	Conversation     *conv.ConversationModel
	Tool             *conv.ToolExecState
	Width            int
	Height           int
	Cwd              string
	InputTokens      int
	OutputTokens     int
	TurnInputTokens  int
	TurnOutputTokens int
	ConversationCost llm.Money

	// Domain services. Commands read live state from these — never snapshot
	// at deps construction time, since /something might mutate state that a
	// later command reads (e.g. /disabled-tools then /tools).
	Setting *setting.Settings
	LLM     *llm.Conn
	Session *session.Setup
	Skill   *skill.Registry
	Plugin  *plugin.Registry
	MCP     *mcp.Registry
	Tracker tracker.Service
	Cron    *cron.Scheduler
	ToolSvc *tool.Registry
	Command *command.Registry

	// Env-state callbacks. `m.env` lives in the parent app package and
	// can't be imported here without a cycle, so its reads/writes are
	// surfaced as callbacks.
	GetThinkingEffort func() string
	SetThinkingEffort func(string)
	ResetTokens       func()

	// Model-level action callbacks. These compose multiple services or
	// touch UI state on `m`, so commands invoke them via the model.
	CommitMessages          func() []tea.Cmd
	SubmitToAgent           func(content string, images []core.Image) tea.Cmd
	HandleSkillInvocation   func() tea.Cmd
	StartExternalEditor     func(path string) tea.Cmd
	ReloadPluginBackedState func() error
	PersistSession          func() error
	InitTaskStorage         func()
	ReconfigureAgentTool    func()
	StopAgentSession        func()
	FireSessionEnd          func(reason string)
	BuildCompactRequest     func(focus, trigger string) conv.CompactRequest
	SpinnerTickCmd          func() tea.Cmd
	ResetCronQueue          func()
	ForkSession             func() (originalSessionID string, err error)
	RunSelfLearnDemo        func()
	GetSystemPrompt         func() string
}

type SlashCommandController struct {
	env SlashCommandEnv
}

func NewSlashCommandController(env SlashCommandEnv) SlashCommandController {
	return SlashCommandController{env: env}
}

func builtinCommandHandlers() map[string]slashCommandHandler {
	return map[string]slashCommandHandler{
		"model":          (*SlashCommandController).handleModelCommand,
		"clear":          (*SlashCommandController).handleClearCommand,
		"fork":           (*SlashCommandController).handleForkCommand,
		"resume":         (*SlashCommandController).handleResumeCommand,
		"help":           (*SlashCommandController).handleHelpCommand,
		"glob":           (*SlashCommandController).handleGlobCommand,
		"tools":          (*SlashCommandController).handleToolCommand,
		"skills":         (*SlashCommandController).handleSkillCommand,
		"agents":         (*SlashCommandController).handleAgentCommand,
		"tokenlimit":     (*SlashCommandController).handleTokenLimitCommand,
		"compact":        (*SlashCommandController).handleCompactCommand,
		"init":           (*SlashCommandController).handleInitCommand,
		"memory":         (*SlashCommandController).handleMemoryCommand,
		"mcp":            (*SlashCommandController).handleMCPCommand,
		"plugin":         (*SlashCommandController).handlePluginCommand,
		"reload-plugins": (*SlashCommandController).handleReloadPluginsCommand,
		"think":          (*SlashCommandController).handleThinkCommand,
		"loop":           (*SlashCommandController).handleLoopCommand,
		"search":         (*SlashCommandController).handleSearchCommand,
		"identity":       (*SlashCommandController).handleIdentityCommand,
		"config":         (*SlashCommandController).handleConfigCommand,
		"context":        (*SlashCommandController).handleContextCommand,
		"selflearn-demo": (*SlashCommandController).handleSelflearnDemoCommand,
	}
}

func (c SlashCommandController) Execute(ctx context.Context, inputText string) (string, tea.Cmd, bool) {
	cmdName, args, isCmd := command.ParseCommand(inputText)
	if !isCmd {
		return "", nil, false
	}

	if result, followUp, handled := c.executeExitCommand(cmdName); handled {
		return result, followUp, true
	}

	if result, followUp, handled := c.executeBuiltinCommand(ctx, cmdName, args); handled {
		return result, followUp, true
	}

	if sk, ok := lookupSkill(c.env.Skill, cmdName); ok {
		return c.executeSkillSlashCommand(sk, args), c.env.HandleSkillInvocation(), true
	}

	if pc, ok := c.env.Command.IsCustomCommand(cmdName); ok {
		return c.executeCustomCommand(pc, args), c.env.HandleSkillInvocation(), true
	}

	return unknownCommandResult(cmdName), nil, true
}

func (c SlashCommandController) HandleSubmit(inputText string) (tea.Cmd, bool) {
	preserve := shouldPreserveCommandInConversation(inputText)
	if preserve {
		c.env.Conversation.Append(core.ChatMessage{Role: core.RoleUser, Content: inputText})
	}

	result, cmd, isCmd := c.Execute(context.Background(), inputText)
	if !isCmd {
		if preserve {
			msgs := c.env.Conversation.Messages
			c.env.Conversation.Messages = msgs[:len(msgs)-1]
		}
		return nil, false
	}

	c.env.Input.Reset()

	if result != "" {
		c.env.Conversation.AddNotice(result)
	}

	cmds := c.env.CommitMessages()
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...), true
}

func (c SlashCommandController) executeBuiltinCommand(ctx context.Context, cmdName, args string) (string, tea.Cmd, bool) {
	handler, ok := builtinCommandHandlers()[cmdName]
	if !ok {
		return "", nil, false
	}
	result, followUp, err := handler(&c, ctx, args)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil, true
	}
	return result, followUp, true
}

func (c SlashCommandController) executeExitCommand(cmdName string) (string, tea.Cmd, bool) {
	if cmdName != "exit" && cmdName != "quit" {
		return "", nil, false
	}
	c.env.StopAgentSession()
	c.env.Conversation.Stream.Stop()
	if c.env.Tool.Cancel != nil {
		c.env.Tool.Cancel()
	}
	c.env.FireSessionEnd("prompt_input_exit")
	return "", tea.Quit, true
}

func (c SlashCommandController) executeSkillSlashCommand(sk *skill.Skill, args string) string {
	if c.env.Skill != nil {
		c.env.Input.Skill.SetPending(sk.FullName(), c.env.Skill.GetSkillInvocationPrompt(sk.FullName()))
	}
	if c.env.Plugin != nil {
		c.env.Input.Skill.PendingPluginRoot = plugin.FindPluginRootForPath(sk.SkillDir)
	}
	c.env.Input.Skill.PendingArgs = formatSlashInvocation(sk.FullName(), args)
	return ""
}

func (c SlashCommandController) executeCustomCommand(pc *command.CustomCommand, args string) string {
	if instructions := pc.GetInstructions(); instructions != "" {
		c.env.Input.Skill.SetPending(pc.FullName(), command.WrapInvocation(pc.FullName(), instructions))
	}
	if c.env.Plugin != nil {
		c.env.Input.Skill.PendingPluginRoot = plugin.FindPluginRootForPath(pc.FilePath)
	}
	c.env.Input.Skill.PendingArgs = formatSlashInvocation(pc.FullName(), args)
	return ""
}

// formatSlashInvocation renders "/<name>" or "/<name> <args>".
func formatSlashInvocation(name, args string) string {
	if args == "" {
		return "/" + name
	}
	return "/" + name + " " + args
}

func (c *SlashCommandController) handleHelpCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	var sb strings.Builder
	sb.WriteString("Available Commands:\n\n")
	builtins := c.env.Command.BuiltinNames()
	names := make([]string, 0, len(builtins))
	for name := range builtins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		info := builtins[name]
		fmt.Fprintf(&sb, "  /%s - %s\n", info.Name, info.Description)
	}
	pluginCmds := c.env.Command.GetCustomCommands()
	if len(pluginCmds) > 0 {
		sb.WriteString("\nCustom Commands:\n\n")
		for _, cmd := range pluginCmds {
			desc := cmd.Description
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Fprintf(&sb, "  /%s - %s\n", cmd.Name, desc)
		}
	}
	return sb.String(), nil, nil
}

func (c *SlashCommandController) handleClearCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	c.env.StopAgentSession()
	c.env.Conversation.Stream.Stop()
	if c.env.Tool.Cancel != nil {
		c.env.Tool.Cancel()
	}
	c.env.Tool.Reset()
	c.env.Conversation.Clear()
	c.env.ResetTokens()
	c.env.Tracker.Reset()
	c.env.ResetCronQueue()
	cmds := []tea.Cmd{tea.ClearScreen}
	if os.Getenv("TMUX") != "" {
		cmds = append(cmds, func() tea.Msg {
			_ = exec.Command("tmux", "clear-history").Run()
			return nil
		})
	}
	return "", tea.Batch(cmds...), nil
}

func (c *SlashCommandController) handleForkCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if len(c.env.Conversation.Messages) == 0 {
		return "Nothing to fork — no messages in current session.", nil, nil
	}
	if err := c.env.PersistSession(); err != nil {
		return "", nil, fmt.Errorf("failed to save session before fork: %w", err)
	}
	if c.env.Session.ID() == "" {
		return "No active session to fork.", nil, nil
	}
	originalID, err := c.env.ForkSession()
	if err != nil {
		return "", nil, fmt.Errorf("failed to fork session: %w", err)
	}
	c.env.InitTaskStorage()
	c.env.ReconfigureAgentTool()
	return fmt.Sprintf("Forked conversation. You are now in the fork.\nTo resume the original: san -r %s", originalID), nil, nil
}

func (c *SlashCommandController) handleResumeCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if err := c.env.Session.EnsureStore(c.env.Cwd); err != nil {
		return "", nil, fmt.Errorf("failed to initialize session store: %w", err)
	}
	if err := c.env.Input.Session.Selector.EnterSelect(c.env.Width, c.env.Height, c.env.Session.GetStore(), c.env.Cwd); err != nil {
		return "", nil, fmt.Errorf("failed to open session selector: %w", err)
	}
	return "", nil, nil
}

// handleConfigCommand opens the /config Self-Learning panel. Currently the
// panel only hosts the L1 config; a multi-panel sidebar (Provider /
// Permissions / Appearance / …) is planned and will be added by extending
// ConfigSelector with sibling panels — the slash command stays /config
// either way.
func (c *SlashCommandController) handleConfigCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	c.env.Input.Config.Enter(c.env.Width, c.env.Height)
	return "", nil, nil
}

// handleSelflearnDemoCommand drives the L1 status-bar indicator through
// its phases (reviewing → recording actions → done) without firing a
// real review fork. Hidden from the slash-command catalog; meant for
// developers checking the spinner / target / done-summary look in their
// own terminal.
func (c *SlashCommandController) handleSelflearnDemoCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if c.env.RunSelfLearnDemo == nil {
		return "", nil, nil
	}
	c.env.RunSelfLearnDemo()
	return "", nil, nil
}

func (c *SlashCommandController) handleSearchCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if err := c.env.Input.Search.Enter(c.env.LLM.Store(), c.env.Width, c.env.Height); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

// handleIdentityCommand dispatches:
//   - /identity            → open selector
//   - /identity create [hint] → inject create workflow as PendingInstructions
//   - /identity edit <name>   → inject edit workflow with target
//
// Subcommands reuse the same skill-invocation pipeline as user-defined
// commands — the workflow body lives in internal/command/builtin/.
func (c *SlashCommandController) handleIdentityCommand(ctx context.Context, args string) (string, tea.Cmd, error) {
	sub, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	rest = strings.TrimSpace(rest)
	switch sub {
	case "":
		_, err := c.env.Input.Identity.Enter(ctx, c.env.Width, c.env.Height)
		return "", nil, err
	case "create":
		c.injectIdentityWorkflow("create", rest)
		return "", c.env.HandleSkillInvocation(), nil
	case "edit":
		if rest == "" {
			return "Usage: /identity edit <name>", nil, nil
		}
		c.injectIdentityWorkflow("edit", rest)
		return "", c.env.HandleSkillInvocation(), nil
	default:
		return "Usage: /identity [create | edit <name>]", nil, nil
	}
}

// injectIdentityWorkflow loads an embedded workflow template, substitutes
// $ARGUMENTS, and primes the skill invocation pipeline so the next user
// submit prepends the workflow as hidden instructions.
func (c *SlashCommandController) injectIdentityWorkflow(sub, args string) {
	body := command.BuiltinWorkflow("identity-" + sub)
	if body == "" {
		return
	}
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)
	name := "identity " + sub
	c.env.Input.Skill.SetPending(name, command.WrapInvocation(name, body))
	c.env.Input.Skill.PendingArgs = formatSlashInvocation(name, args)
}

func (c *SlashCommandController) handleModelCommand(ctx context.Context, _ string) (string, tea.Cmd, error) {
	cmd, err := c.env.Input.Provider.Selector.Enter(ctx, c.env.Width, c.env.Height)
	if err != nil {
		return "", nil, err
	}
	return "", cmd, nil
}

func (c *SlashCommandController) handleInitCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	result, err := HandleInitCommand(c.env.Cwd, args)
	return result, nil, err
}

func (c *SlashCommandController) handleMemoryCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	result, editPath, err := HandleMemoryCommand(&c.env.Input.Memory.Selector, c.env.Cwd, c.env.Width, c.env.Height, args)
	if err != nil {
		return "", nil, err
	}
	if editPath != "" {
		c.env.Input.Memory.EditingFile = editPath
		return result, c.env.StartExternalEditor(editPath), nil
	}
	return result, nil, nil
}

func (c *SlashCommandController) handleMCPCommand(ctx context.Context, args string) (string, tea.Cmd, error) {
	result, editInfo, err := HandleMCPCommand(ctx, &c.env.Input.MCP.Selector, c.env.Width, c.env.Height, args)
	if err != nil {
		return "", nil, err
	}
	if editInfo != nil {
		c.env.Input.MCP.EditingFile = editInfo.TempFile
		c.env.Input.MCP.EditingServer = editInfo.ServerName
		c.env.Input.MCP.EditingScope = editInfo.Scope
		return result, StartMCPEditor(editInfo.TempFile), nil
	}
	if c.env.Input.MCP.Selector.IsActive() {
		return result, c.env.Input.MCP.Selector.AutoReconnect(), nil
	}
	return result, nil, nil
}

func (c *SlashCommandController) handlePluginCommand(ctx context.Context, args string) (string, tea.Cmd, error) {
	result, err := HandlePluginCommand(ctx, &c.env.Input.Plugin, c.env.Cwd, c.env.Width, c.env.Height, args)
	return result, nil, err
}

func (c *SlashCommandController) handleReloadPluginsCommand(ctx context.Context, args string) (string, tea.Cmd, error) {
	if strings.TrimSpace(args) != "" {
		return "Usage: /reload-plugins", nil, nil
	}
	if err := c.env.Plugin.Load(ctx, c.env.Cwd); err != nil {
		return "", nil, fmt.Errorf("failed to reload plugin registry: %w", err)
	}
	_ = c.env.Plugin.LoadClaudePlugins(ctx)
	if err := c.env.ReloadPluginBackedState(); err != nil {
		return "", nil, err
	}
	return "Reloaded plugins and refreshed plugin-backed skills, agents, MCP servers, and hooks.", nil, nil
}

func (c *SlashCommandController) handleGlobCommand(ctx context.Context, args string) (string, tea.Cmd, error) {
	if args == "" {
		return "Usage: /glob <pattern> [path]", nil, nil
	}
	params := map[string]any{"pattern": args}
	parts := strings.SplitN(args, " ", 2)
	if len(parts) == 2 {
		params["pattern"] = parts[0]
		params["path"] = parts[1]
	}
	result := c.env.ToolSvc.Execute(ctx, "glob", params, c.env.Cwd)
	return conv.RenderToolResult(result, c.env.Width), nil, nil
}

func (c *SlashCommandController) handleToolCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	var mcpTools func() []core.ToolSchema
	if c.env.MCP != nil {
		mcpTools = c.env.MCP.GetToolSchemas
	}
	if err := c.env.Input.Tool.EnterSelect(c.env.Width, c.env.Height, c.env.Setting.DisabledTools(), mcpTools); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

func (c *SlashCommandController) handleSkillCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if err := c.env.Input.Skill.Selector.EnterSelect(c.env.Width, c.env.Height); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

func (c *SlashCommandController) handleAgentCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	if err := c.env.Input.Agent.EnterSelect(c.env.Width, c.env.Height); err != nil {
		return "", nil, err
	}
	return "", nil, nil
}

func (c *SlashCommandController) handleThinkCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	model := ""
	if c.env.LLM.CurrentModel() != nil {
		model = c.env.LLM.CurrentModel().ModelID
	}
	efforts := llm.ThinkingEfforts(c.env.LLM.Provider(), model)
	if len(efforts) == 0 {
		return "Current provider does not support thinking effort.", nil, nil
	}

	arg := strings.TrimSpace(strings.ToLower(args))
	var effort string
	if arg == "" || arg == "toggle" {
		next, _ := llm.NextThinkingEffort(c.env.LLM.Provider(), model, c.env.GetThinkingEffort())
		effort = next
	} else {
		if arg == "off" && !containsThinkingEffort(efforts, "off") && containsThinkingEffort(efforts, "none") {
			arg = "none"
		}
		for _, allowed := range efforts {
			if strings.EqualFold(arg, allowed) {
				effort = allowed
				break
			}
		}
		if effort == "" {
			return fmt.Sprintf("Usage: /think [%s]\n\nWithout arguments, cycles to the next effort.", strings.Join(efforts, "|")), nil, nil
		}
	}

	c.env.SetThinkingEffort(effort)
	token := c.env.Input.Provider.SetStatusMessage(fmt.Sprintf("thinking: %s", effort))
	return "", kit.StatusTimer(3*time.Second, token), nil
}

func containsThinkingEffort(efforts []string, effort string) bool {
	for _, allowed := range efforts {
		if strings.EqualFold(allowed, effort) {
			return true
		}
	}
	return false
}

func (c *SlashCommandController) handleLoopCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return loopUsage(), nil, nil
	}
	if result, handled, err := handleLoopAdminCommand(c.env.Cron, args); handled {
		return result, nil, err
	}
	if strings.HasPrefix(strings.ToLower(args), "once ") {
		parsed, err := cron.ParseLoopOnceCommand(strings.TrimSpace(args[5:]), time.Now())
		if err != nil {
			return loopUsage(), nil, nil
		}
		job, err := c.env.Cron.Create(parsed.Cron, parsed.Prompt, false, false)
		if err != nil {
			return "", nil, err
		}
		if c.env.Conversation.Messages == nil {
			*c.env.Conversation = conv.NewConversation()
		}
		c.env.Conversation.AddNotice(fmt.Sprintf("Scheduled one-shot task %s (%s, cron `%s`).%s It will fire once and auto-delete.", job.ID, parsed.Human, parsed.Cron, parsed.Note))
		return "", nil, nil
	}
	parsed, err := cron.ParseLoopCommand(args, time.Now())
	if err != nil {
		return loopUsage(), nil, nil
	}
	job, err := c.env.Cron.Create(parsed.Cron, parsed.Prompt, true, false)
	if err != nil {
		return "", nil, err
	}
	if c.env.Conversation.Messages == nil {
		*c.env.Conversation = conv.NewConversation()
	}
	c.env.Conversation.AddNotice(fmt.Sprintf("Scheduled recurring task %s (%s, cron `%s`).%s Auto-expires after 7 days. Executing now.", job.ID, parsed.Human, parsed.Cron, parsed.Note))
	c.env.Conversation.Append(core.ChatMessage{Role: core.RoleUser, Content: parsed.Prompt})
	return "", c.env.SubmitToAgent(parsed.Prompt, nil), nil
}

func handleLoopAdminCommand(cronSvc *cron.Scheduler, args string) (string, bool, error) {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return "", false, nil
	}
	switch strings.ToLower(fields[0]) {
	case "list", "ls":
		return renderLoopJobList(cronSvc), true, nil
	case "delete", "del", "rm", "remove", "cancel":
		if len(fields) < 2 {
			return "Usage: /loop delete <job-id>", true, nil
		}
		if strings.EqualFold(fields[1], "all") {
			jobs := cronSvc.List()
			for _, job := range jobs {
				if err := cronSvc.Delete(job.ID); err != nil {
					return "", true, err
				}
			}
			return fmt.Sprintf("Cancelled %d scheduled task(s).", len(jobs)), true, nil
		}
		id := strings.TrimSpace(fields[1])
		if id == "" {
			return "Usage: /loop delete <job-id>", true, nil
		}
		if err := cronSvc.Delete(id); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("Cancelled scheduled task %s.", id), true, nil
	default:
		return "", false, nil
	}
}

func renderLoopJobList(cronSvc *cron.Scheduler) string {
	jobs := cronSvc.List()
	if len(jobs) == 0 {
		return "No scheduled loop tasks."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d scheduled loop task(s):\n\n", len(jobs)))
	for _, job := range jobs {
		mode := "recurring"
		if !job.Recurring {
			mode = "one-shot"
		}
		if job.Durable {
			mode += ", durable"
		}
		sb.WriteString(fmt.Sprintf("%s  %s (%s)\n", job.ID, cron.Describe(job.Cron), mode))
		sb.WriteString(fmt.Sprintf("  Cron: %s\n", job.Cron))
		sb.WriteString(fmt.Sprintf("  Prompt: %s\n", job.Prompt))
		sb.WriteString(fmt.Sprintf("  Next: %s\n\n", job.NextFire.Format("2006-01-02 15:04")))
	}
	return sb.String()
}

func loopUsage() string {
	return "Usage: /loop [interval] <prompt>\n       /loop once <interval> <prompt>\n       /loop once <prompt> in <interval>\n       /loop list\n       /loop delete <job-id>\n       /loop delete all\nExamples: /loop 5m check the deploy, /loop check the deploy every 20m, /loop once 20m check the deploy"
}

func (c *SlashCommandController) handleTokenLimitCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	result, cmd, err := HandleTokenLimitCommand(TokenLimitDeps{
		CurrentModel: c.env.LLM.CurrentModel(),
		Provider:     c.env.LLM.Provider(),
		Store:        c.env.LLM.Store(),
		InputTokens:  c.env.InputTokens,
		Cwd:          c.env.Cwd,
		SpinnerTick:  c.env.SpinnerTickCmd(),
		ToolSvc:      c.env.ToolSvc,
	}, args)
	if cmd != nil {
		c.env.Input.Provider.FetchingLimits = true
	}
	return result, cmd, err
}

func (c *SlashCommandController) handleContextCommand(_ context.Context, _ string) (string, tea.Cmd, error) {
	msgCount := len(c.env.Conversation.Messages)
	toolCount := 0
	var skills []*skill.Skill
	if c.env.Skill != nil {
		skills = c.env.Skill.List()
	}
	if c.env.ToolSvc != nil {
		toolCount = len(c.env.ToolSvc.List())
	}
	sysPrompt := ""
	if c.env.GetSystemPrompt != nil {
		sysPrompt = c.env.GetSystemPrompt()
	}
	result := FormatContextInfo(ContextDeps{
		CurrentModel:     c.env.LLM.CurrentModel(),
		Store:            c.env.LLM.Store(),
		InputTokens:      c.env.InputTokens,
		OutputTokens:     c.env.OutputTokens,
		TurnInputTokens:  c.env.TurnInputTokens,
		TurnOutputTokens: c.env.TurnOutputTokens,
		ConversationCost: c.env.ConversationCost,
		MessageCount:     msgCount,
		ToolCount:        toolCount,
		Skills:           skills,
		SystemPrompt:     sysPrompt,
	})
	return result, nil, nil
}

func (c *SlashCommandController) handleCompactCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	if c.env.LLM.Provider() == nil {
		return NoProviderMsg, nil, nil
	}
	if len(c.env.Conversation.Messages) == 0 {
		return "No active LLM session. Send a message first to initialize the client.", nil, nil
	}
	if len(c.env.Conversation.Messages) < 3 {
		return "Not enough conversation history to compact.", nil, nil
	}
	if c.env.Conversation.Stream.Active {
		return "Cannot compact while streaming.", nil, nil
	}
	c.env.Conversation.Compact.Active = true
	c.env.Conversation.Compact.SummaryFocus = strings.TrimSpace(args)
	c.env.Conversation.Compact.Phase = conv.PhaseSummarizing
	c.env.Conversation.Compact.Count = len(c.env.Conversation.ConvertToProvider())
	return "", tea.Batch(c.env.SpinnerTickCmd(), conv.CompactCmd(c.env.BuildCompactRequest(c.env.Conversation.Compact.SummaryFocus, "manual"))), nil
}

func lookupSkill(svc *skill.Registry, cmd string) (*skill.Skill, bool) {
	if svc == nil {
		return nil, false
	}
	sk, ok := svc.Get(cmd)
	if !ok || !sk.IsEnabled() {
		return nil, false
	}
	return sk, true
}

func LookupSkillCommand(cmd string) (*skill.Skill, bool) {
	return lookupSkill(skill.DefaultIfInit(), cmd)
}

func unknownCommandResult(cmd string) string {
	return fmt.Sprintf("Unknown command: /%s\nType /help for available commands.", cmd)
}

func SkillCommandInfos() []command.Info {
	svc := skill.DefaultIfInit()
	if svc == nil {
		return nil
	}
	enabled := svc.GetEnabled()
	infos := make([]command.Info, 0, len(enabled))
	for _, sk := range enabled {
		description := sk.Description
		if sk.ArgumentHint != "" {
			description += " " + sk.ArgumentHint
		}
		infos = append(infos, command.Info{Name: sk.FullName(), Description: description})
	}
	return infos
}

func shouldPreserveCommandInConversation(inputText string) bool {
	name, _, isCmd := command.ParseCommand(inputText)
	if !isCmd {
		return false
	}
	switch name {
	case "compact", "fork", "resume", "loop", "init", "tokenlimit":
		return true
	}
	return false
}
