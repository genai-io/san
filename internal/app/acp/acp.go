package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genai-io/san/internal/app/ext"
	apphook "github.com/genai-io/san/internal/app/hook"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/infra/filecache"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/plugin"
	agentruntime "github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool/fs"
	_ "github.com/genai-io/san/internal/tool/register" // ACP serves the built-in tools without the TUI package
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"go.uber.org/zap"
)

// acpWriteTimeout is a per-active-frame budget, not a prompt deadline. A local
// editor should drain even a 64 KiB tool preview well within 30 seconds. Expiry
// sacrifices the connection (all sessions), rather than claiming delivery.
const acpWriteTimeout = 30 * time.Second

// Run serves the native agent over ACP. The SDK owns framing, request
// admission and cancellation; this host owns native session resources.
// Closing input/output must interrupt active I/O; raw blocking standard file
// descriptors do not satisfy this contract. The CLI supplies pollable streams.
func Run(ctx context.Context, input io.ReadCloser, output io.WriteCloser, version string) error {
	llm.Initialize()
	provider, model, err := llm.ResolveProviderModel(ctx)
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
const acpShellDirHelp = "Directory changes affect only this command; subsequent commands start in the original session working directory"

// Each SDK-admitted session has independent policy, transcript and history.
// The SDK serializes prompts for a session, including the response barrier.
type acpSession struct {
	input              acpInput
	nativeSession      agentruntime.MainSession
	runtimeBuildParams agentruntime.BuildParams // retained across runtime rebuilds
	runtime            agentruntime.Runtime
	hooks              *hook.Engine
	reminders          *reminder.Queue
	toolResponses      sync.Map
	mcp                *mcp.ServerSet
	permissions        *acpPermissions
	commands           *command.Registry
	lastSentCommands   []command.Info // Last command list sent to the client; unchanged lists are not resent.
	agents             *subagent.Registry
	models             []llm.ModelInfo
	tracker            *todo.Store
}

func (h *acpHost) create(ctx context.Context, req protocol.NewSessionRequest) (acp.CreateSessionResult, error) {
	response, err := h.open(ctx, req, "")
	if err != nil {
		return acp.CreateSessionResult{}, err
	}
	h.mu.Lock()
	s := h.sessions[response.SessionID]
	h.mu.Unlock()
	catalog := s.commands.CustomCommands()
	s.lastSentCommands = catalog
	return acp.CreateSessionResult{NewSessionResponse: response, Commands: acpCommandCatalog(catalog)}, nil
}

func (h *acpHost) open(ctx context.Context, req protocol.NewSessionRequest, resumeID string) (_ protocol.NewSessionResponse, err error) {
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
	store, err := session.OpenStore(ctx, req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	var opened *session.Session
	var history *session.Projection
	defer func() {
		if err == nil {
			return
		}
		if opened != nil {
			err = errors.Join(err, opened.Close())
			if resumeID == "" {
				// Roll back only this unpublished new session, never a loaded one.
				err = errors.Join(err, store.Delete(context.WithoutCancel(ctx), opened.ID()))
			}
		}
		err = errors.Join(err, store.Close())
	}()
	if resumeID != "" {
		opened, err = store.Open(ctx, resumeID)
		if err != nil {
			return protocol.NewSessionResponse{}, err
		}
		snapshot := opened.Snapshot()
		history = &snapshot
		if err := session.ValidateResumeDirectory(history.ExecutionCWD(), req.CWD); err != nil {
			return protocol.NewSessionResponse{}, err
		}
	}
	settings, err := setting.LoadForCwd(req.CWD)
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
	if history != nil && history.Config().Model != nil && *history.Config().Model != "" {
		saved := history.Config()
		found = false
		for _, m := range models {
			if m.ID == *saved.Model {
				found = true
			}
		}
		if !found || (saved.Provider != nil && *saved.Provider != "" && *saved.Provider != h.provider.Name()) {
			return protocol.NewSessionResponse{}, fmt.Errorf("saved model is unavailable from the configured provider")
		}
		modelID = *saved.Model
	}
	plugins, skills, servers, err := acpExtensions(ctx, req.CWD, settings, req.MCPServers)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	ctx = plugin.WithRegistry(ctx, plugins)
	s := &acpSession{reminders: reminder.NewQueue(), mcp: servers, models: models}
	pluginSource := ext.Plugin{Registry: plugins}
	s.commands = command.NewRegistry(command.Options{CWD: req.CWD, PluginCommandPaths: pluginSource.CommandPaths})
	s.agents, err = subagent.Load(req.CWD, pluginSource.AgentPaths())
	if err != nil {
		_ = servers.Close()
		return protocol.NewSessionResponse{}, err
	}
	if opened == nil {
		opened, err = store.Create(ctx, session.CreateOptions{InitialCWD: req.CWD, Config: &session.EventConfig{Provider: new(h.provider.Name()), Model: new(h.model), GenerationOptions: &session.GenerationOptions{Version: 1, MaxTokens: new(setting.DefaultMaxTokens)}}})
		if err != nil {
			_ = servers.Close()
			return protocol.NewSessionResponse{}, err
		}
	}
	s.nativeSession = agentruntime.MainSession{Session: opened, Store: store}
	tasks := task.NewRunSet()
	s.tracker = todo.NewStoreWithTasks(tasks)
	if home, err := os.UserHomeDir(); err == nil {
		if err := s.tracker.SetStorageDir(task.ListDir(home, opened.ID())); err != nil {
			log.Logger().Warn("task tracker storage unavailable", zap.Error(err))
		}
	}
	s.reminders.Register(reminder.ProviderSkillsDirectory, skills.PromptSection)
	if err := s.nativeSession.Validate(); err != nil {
		_ = servers.Close()
		return protocol.NewSessionResponse{}, err
	}
	s.hooks = hook.NewEngine(settings, opened.ID(), req.CWD, opened.Path())
	apphook.Bind(s.hooks, h.provider, opened, modelID)
	skills.SetStateChangeObserver(session.NewRecorder(opened, h.provider.Name()).RecordSkillStateChange)
	s.hooks.SetEnvProvider(func(ctx context.Context) []string { return plugin.PluginEnv(plugin.WithRegistry(ctx, plugins)) })
	s.permissions = &acpPermissions{SessionPermissions: setting.NewSessionPermissions(), settings: settings}
	s.permissions.AddWorkingDirectory(req.CWD)
	if history != nil {
		if saved := history.Config().Mode; saved != nil && *saved == "auto-accept" {
			s.permissions.SetMode(setting.ModeAutoAccept)
		}
	}
	var memory system.MemoryStore
	if setting.New(settings).AutoMemory() {
		memory = setting.MemoryStoreFor(req.CWD)
	}
	policy := s.permissions.ForDirectory(req.CWD)
	s.runtimeBuildParams = agentruntime.BuildParams{SessionID: opened.ID(), CWD: req.CWD, Provider: h.provider, ModelID: modelID,
		MaxTokens: setting.DefaultMaxTokens, Skills: skills, Plugins: plugins, PluginRoot: s.runtime.PluginRoot,
		MCPTools:  mcp.AsCoreTools(servers.ToolSchemas(), servers),
		FileViews: fs.NewFileViews(), ShellDirHelp: acpShellDirHelp,
		ToolResponse: func(callID string, response any) { s.toolResponses.Store(callID, response) },
		Tasks:        tasks, NativeTools: tasks.Tools(), Tracker: s.tracker,
		PermissionRules:  policy.Decide,
		CanApproveByHook: policy.CanApproveByHook,
		HookEngine:       s.hooks, Recording: opened,
		ResultFilter: agentruntime.ResultLimiter{Store: store, SessionID: opened.ID()}.Filter,
		AutoMemory:   agentruntime.AutoMemoryConfig{Store: memory, Recall: settings.MemoryRecall(), IndexLimits: settings.MemoryPreview()},
		FileCache:    filecache.New(),
	}
	if history != nil {
		if generation := history.Config().GenerationOptions; generation != nil {
			if generation.MaxTokens != nil {
				s.runtimeBuildParams.MaxTokens = *generation.MaxTokens
			}
			s.runtimeBuildParams.ThinkingEffort = string(generation.Effort)
		}
	}
	// Tools requiring the TUI or process-global extension state are not offered.
	s.runtimeBuildParams.DisabledTools = setting.WithDefaultDisabledTools(settings.DisabledTools)
	for _, schema := range s.runtimeBuildParams.Schemas() {
		switch schema.Name {
		case "AskUserQuestion":
			if !h.forms {
				s.runtimeBuildParams.DisabledTools[schema.Name] = true
			}
		case "Read", "Write", "Edit", "Bash", "PowerShell", "Glob", "Grep", "Skill", "Agent", "TaskCreate", "TaskGet", "TaskUpdate":
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
		s.reminders.RestoreHistory(history.ActiveMessages())
		source = "resume"
	}
	outcome := s.hooks.Execute(ctx, hook.SessionStart, hook.HookInput{Source: source, Model: modelID})
	var hookRecording *hook.RecordingError
	var completionRecording *llm.RecordingError
	if errors.As(outcome.Error, &hookRecording) || errors.As(outcome.Error, &completionRecording) {
		_ = s.closeRuntime()
		return protocol.NewSessionResponse{}, outcome.Error
	}
	if outcome.AdditionalContext != "" {
		s.reminders.EnqueueWithSource(outcome.AdditionalContext, reminder.SourceSessionStart)
	}
	if err := ctx.Err(); err != nil {
		s.hooks.Shutdown()
		_ = servers.Close()
		return protocol.NewSessionResponse{}, err
	}
	if history != nil {
		if err := s.configureAgents(); err != nil {
			_ = s.closeRuntime()
			return protocol.NewSessionResponse{}, err
		}
		if _, err := s.runtime.Resume(s.runtimeBuildParams); err != nil {
			_ = s.closeRuntime()
			return protocol.NewSessionResponse{}, err
		}
	}
	if err := s.recordState(); err != nil {
		_ = s.closeRuntime()
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
	return errors.Join(s.closeRuntime(), s.nativeSession.Session.Close(), s.nativeSession.Store.Close())
}

func (s *acpSession) closeRuntime() error {
	if gate := s.runtime.PermissionGate(); gate != nil {
		gate.Close()
	}
	err := s.runtime.Reset(context.Background())
	if s.runtimeBuildParams.Tasks != nil {
		s.runtimeBuildParams.Tasks.Cancel()
		s.runtimeBuildParams.Tasks.Join()
	}
	s.toolResponses.Clear()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.hooks.Execute(ctx, hook.SessionEnd, hook.HookInput{Reason: "connection_closed"})
	s.hooks.Shutdown()
	if s.mcp != nil {
		err = errors.Join(err, s.mcp.Close())
	}
	return err
}

func (s *acpSession) refreshMemory() {
	user, project := system.FormatInstructions(system.LoadMemoryFiles(s.runtimeBuildParams.CWD))
	s.reminders.Register(reminder.ProviderMemoryUser, func() string { return user })
	s.reminders.Register(reminder.ProviderMemoryProject, func() string { return project })
}

func (s *acpSession) recordState() error {
	return session.NewRecorder(s.nativeSession.Session, s.runtimeBuildParams.Provider.Name()).RecordState(context.Background(), session.SessionUpdatePayload{
		Config: &session.ConfigPatch{Provider: session.SetField(s.runtimeBuildParams.Provider.Name()), Model: session.SetField(s.runtimeBuildParams.ModelID), Mode: session.SetField(s.permissions.CurrentMode().PersistenceName())},
		State:  &session.StatePatch{ExecutionCWD: session.SetField(s.runtimeBuildParams.CWD)}})
}

func acpText(text string) protocol.ContentBlock {
	return protocol.ContentBlock{Value: protocol.ContentBlockTextVariant{Type: "text", Text: text}}
}
