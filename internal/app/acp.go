package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// acpWriteTimeout is a per-active-frame budget, not a prompt deadline. A local
// editor should drain even a 64 KiB tool preview well within 30 seconds. Expiry
// sacrifices the connection (all sessions), rather than claiming delivery.
const acpWriteTimeout = 30 * time.Second

// RunACP serves the native agent over ACP. The SDK owns framing, request
// admission and cancellation; this host owns native session resources.
// Closing input/output must interrupt active I/O; raw blocking standard file
// descriptors do not satisfy this contract. The CLI supplies pollable streams.
func RunACP(ctx context.Context, input io.ReadCloser, output io.WriteCloser, version string) error {
	llm.Initialize()
	provider, model, err := resolveProvider(ctx)
	if err != nil {
		return err
	}
	host := &acpHost{provider: provider, model: model, sessions: make(map[protocol.SessionID]*acpSession)}
	endpoint, err := host.start(ctx, input, output, version, acpWriteTimeout)
	if err != nil {
		return err
	}
	return host.wait(endpoint)
}

func (h *acpHost) start(ctx context.Context, input io.ReadCloser, output io.WriteCloser, version string, writeTimeout time.Duration) (*acp.Agent, error) {
	return acp.NewAgent(ctx, input, output, acp.AgentHandlers{
		Initialize: func(_ context.Context, req protocol.InitializeRequest) (protocol.InitializeResponse, error) {
			h.mu.Lock()
			caps := req.ClientCapabilities.Value.Elicitation
			h.forms = caps.Present && !caps.Null && caps.Value.Form.Present && !caps.Value.Form.Null
			h.mu.Unlock()
			return protocol.InitializeResponse{ProtocolVersion: 1, AgentCapabilities: protocol.Some(protocol.AgentCapabilities{LoadSession: protocol.Some(true), MCPCapabilities: protocol.Some(protocol.MCPCapabilities{HTTP: protocol.Some(true), SSE: protocol.Some(true)})}), AgentInfo: protocol.Some(protocol.Implementation{Name: "san", Version: version})}, nil
		},
		CreateSession: h.create,
		LoadSession:   h.load,
		SetMode:       h.setMode,
		SetConfig:     h.setConfig,
		Prompt:        h.prompt,
	}, acp.AgentOptions{Limits: acp.Limits{WriteTimeout: writeTimeout}})
}

func (h *acpHost) wait(endpoint *acp.Agent) error {
	<-endpoint.Done()
	// Wait joins callbacks before any session writer is closed.
	err := endpoint.Wait(context.Background())
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	for _, s := range h.sessions {
		err = errors.Join(err, s.close())
	}
	return err
}

type acpHost struct {
	forms    bool
	provider llm.Provider
	model    string
	mu       sync.Mutex
	sessions map[protocol.SessionID]*acpSession
}

// ACP does not apply shell directory handoffs to the session's CWD.
const acpBashDirHelp = "Directory changes affect only this command; subsequent commands start in the original session working directory"

// Each SDK-admitted session has independent policy, transcript and history.
// The SDK serializes prompts for a session, including the response barrier.
type acpSession struct {
	input                   acpInput
	nativeSession           agent.MainSession
	runtimeBuildParams      agent.BuildParams // retained across runtime rebuilds
	runtime                 agent.Runtime
	hooks                   *hook.Engine
	reminders               *reminder.Reminder
	lastPrompt              string
	sideEffects             sync.Map
	mcp                     *mcp.Manager
	permissions             *setting.SessionPermissions         // sole grant and mode owner
	permissionsForDirectory func(string) agent.PermissionPolicy // directory-scoped rules sharing permissions
	commands                *command.Registry
	agents                  *subagent.Registry
	models                  []llm.ModelInfo
}

func (h *acpHost) create(ctx context.Context, req protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
	return h.open(ctx, req, "")
}

func (h *acpHost) open(ctx context.Context, req protocol.NewSessionRequest, resumeID string) (protocol.NewSessionResponse, error) {
	if !filepath.IsAbs(req.CWD) {
		return protocol.NewSessionResponse{}, fmt.Errorf("session cwd must be absolute")
	}
	info, err := os.Stat(req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	if !info.IsDir() {
		return protocol.NewSessionResponse{}, fmt.Errorf("session cwd must be a directory")
	}
	settings, err := setting.LoadForCwd(req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	store, err := session.NewStore(req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	var opened *session.Session
	var history *session.History
	if resumeID == "" {
		opened, err = store.Create(session.CreateOptions{ID: session.NewSessionID(), Cwd: req.CWD, Provider: h.provider.Name(), Model: h.model, MaxTokens: setting.DefaultMaxTokens})
	} else {
		opened, history, err = store.Open(resumeID)
	}
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	modelID := h.model
	models, _ := h.provider.ListModels(ctx)
	found := false
	for _, m := range models {
		if m.ID == h.model {
			found = true
		}
	}
	if !found {
		models = append(models, llm.ModelInfo{ID: h.model, Name: h.model})
	}
	if history != nil && history.Metadata.Model != "" {
		found = false
		for _, m := range models {
			if m.ID == history.Metadata.Model {
				found = true
			}
		}
		if !found || (history.Metadata.Provider != "" && history.Metadata.Provider != h.provider.Name()) {
			_ = opened.Close()
			return protocol.NewSessionResponse{}, fmt.Errorf("saved model is unavailable from the configured provider")
		}
		modelID = history.Metadata.Model
	}
	plugins, skills, servers, err := acpExtensions(ctx, req.CWD, settings, req.MCPServers)
	if err != nil {
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	ctx = plugin.WithRegistry(ctx, plugins)
	s := &acpSession{nativeSession: agent.MainSession{Session: opened, Store: store}, reminders: reminder.New(), mcp: servers, models: models}
	s.commands = acpCommands(req.CWD, plugins)
	var agentPaths []subagent.PluginAgentPath
	for _, p := range plugins.AgentPaths() {
		agentPaths = append(agentPaths, subagent.PluginAgentPath{Path: p.Path, Namespace: p.Namespace})
	}
	s.agents, err = subagent.Load(req.CWD, agentPaths)
	if err != nil {
		_ = servers.Close()
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	tasks := task.NewManager()
	s.reminders.Register(reminder.ProviderSkillsDirectory, skills.PromptSection)
	if err := s.nativeSession.Validate(); err != nil {
		_ = servers.Close()
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	s.hooks = hook.NewEngine(settings, opened.ID(), req.CWD, opened.TranscriptPath())
	s.hooks.SetAuditCallback(opened.RecordHookAudit)
	s.hooks.SetEnvProvider(func(ctx context.Context) []string { return plugin.PluginEnv(plugin.WithRegistry(ctx, plugins)) })
	s.hooks.SetLLMCompleter(buildHookCompleter(h.provider), modelID)
	audit := func(rec transcript.Record) { _ = opened.Append(context.Background(), rec) }
	policy := agent.PermissionPolicy{Settings: setting.New(settings), Session: setting.NewSessionPermissions(), Audit: audit}
	policy.Session.AddWorkingDirectory(req.CWD)
	s.permissions = policy.Session
	if history != nil {
		s.runtime.SetMessages(history.Messages)
		s.reminders.RestoreHistory(store.ChatRows(history))
		s.lastPrompt = history.Metadata.LastPrompt
		if history.Metadata.Mode == "auto-accept" {
			s.permissions.SetMode(setting.ModeAutoAccept)
		}
	}
	if setting.New(settings).AutoMemory() {
		policy.AutoMemory = setting.MemoryStoresFor(req.CWD)
	}
	s.permissionsForDirectory = func(cwd string) agent.PermissionPolicy {
		scoped := policy
		scoped.Settings = setting.ScopedPermissions{Data: settings, CWD: cwd}
		return scoped
	}
	policy = s.permissionsForDirectory(req.CWD)
	s.runtimeBuildParams = agent.BuildParams{SessionID: opened.ID(), CWD: req.CWD, Provider: h.provider, ModelID: modelID,
		MaxTokens: setting.DefaultMaxTokens, Skills: skills, Plugins: plugins, PluginRoot: s.runtime.PluginRoot,
		MCPTools:  mcp.AsCoreTools(servers.ToolSchemas(), servers),
		FileViews: fs.NewFileViews(), BashDirHelp: acpBashDirHelp,
		ToolResponse: func(callID string, response any) { s.sideEffects.Store(callID, response) },
		Tasks:        tasks, NativeTools: tasks.Tools(),
		PermissionRules:  policy.Decide,
		CanApproveByHook: policy.CanApproveByHook,
		HookEngine:       s.hooks, OnEvent: opened.RecordEvent,
		ResultFilter: agent.ResultPager{Store: store, SessionID: opened.ID()}.Filter,
		AutoMemory:   policy.AutoMemory, FileCache: filecache.New(),
	}
	// Tools requiring the TUI or process-global extension state are not offered.
	s.runtimeBuildParams.DisabledTools = setting.WithDefaultDisabledTools(settings.DisabledTools)
	for _, schema := range s.runtimeBuildParams.Schemas() {
		switch schema.Name {
		case "AskUserQuestion":
			if !h.forms {
				s.runtimeBuildParams.DisabledTools[schema.Name] = true
			}
		case "Read", "Write", "Edit", "Bash", "Glob", "Grep", "Skill", "Agent":
		default:
			s.runtimeBuildParams.DisabledTools[schema.Name] = true
		}
	}
	if h.forms {
		s.runtimeBuildParams.AskUser = s.input.ask
		s.runtimeBuildParams.BashPromptResponder = s.input.responder
	}
	s.refreshMemory()
	source := "startup"
	if history != nil {
		source = "resume"
	}
	outcome := s.hooks.Execute(ctx, hook.SessionStart, hook.HookInput{Source: source, Model: modelID})
	if outcome.AdditionalContext != "" {
		s.reminders.Enqueue(outcome.AdditionalContext)
	}
	if err := ctx.Err(); err != nil {
		s.hooks.Shutdown()
		_ = servers.Close()
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	id := protocol.SessionID(opened.ID())
	h.mu.Lock()
	h.sessions[id] = s
	h.mu.Unlock()
	return protocol.NewSessionResponse{SessionID: id, Modes: protocol.Some(s.modes()), ConfigOptions: protocol.Some(s.configOptions())}, nil
}

func (h *acpHost) prompt(ctx context.Context, turn *acp.Turn, req protocol.PromptRequest) (protocol.PromptResponse, error) {
	h.mu.Lock()
	s := h.sessions[req.SessionID]
	h.mu.Unlock()
	if s == nil {
		return protocol.PromptResponse{}, fmt.Errorf("unknown session")
	}
	return s.prompt(ctx, turn, req)
}

// close runs after the endpoint has joined all session callbacks.
func (s *acpSession) close() error {
	if gate := s.runtime.PermissionGate(); gate != nil {
		gate.Close()
	}
	err := s.runtime.Reset(context.Background())
	if s.runtimeBuildParams.Tasks != nil {
		s.runtimeBuildParams.Tasks.Cancel()
		s.runtimeBuildParams.Tasks.Join()
	}
	s.sideEffects.Clear()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.hooks.Execute(ctx, hook.SessionEnd, hook.HookInput{Reason: "connection_closed"})
	s.hooks.Shutdown()
	if s.mcp != nil {
		err = errors.Join(err, s.mcp.Close())
	}
	return errors.Join(err, s.nativeSession.Session.Close())
}

func (s *acpSession) refreshMemory() {
	var user, project []string
	for _, file := range system.LoadMemoryFiles(s.runtimeBuildParams.CWD) {
		if file.Level == "global" {
			user = append(user, file.Content)
		} else {
			project = append(project, file.Content)
		}
	}
	userText, projectText := strings.Join(user, "\n\n"), strings.Join(project, "\n\n")
	s.reminders.Register(reminder.ProviderMemoryUser, func() string { return reminder.WrapMemory("user", userText) })
	s.reminders.Register(reminder.ProviderMemoryProject, func() string { return reminder.WrapMemory("project", projectText) })
}

func (s *acpSession) recordState() error {
	return s.nativeSession.Session.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched,
		State: &transcript.StatePatch{LastPrompt: new(s.lastPrompt), Mode: new(s.permissions.CurrentMode().PersistenceName())}})
}

func acpText(text string) protocol.ContentBlock {
	return protocol.ContentBlock{Value: protocol.ContentBlockTextVariant{Type: "text", Text: text}}
}
