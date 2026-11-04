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
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// RunACP serves the native agent over ACP. The SDK owns framing, request
// admission and cancellation; this host owns native session resources.
func RunACP(ctx context.Context, input io.ReadCloser, output io.WriteCloser, version string) error {
	llm.Initialize()
	provider, model, err := resolveProvider(ctx)
	if err != nil {
		return err
	}
	host := &acpHost{provider: provider, model: model, sessions: make(map[protocol.SessionID]*acpSession)}
	endpoint, err := acp.NewAgent(ctx, input, output, acp.AgentHandlers{
		Initialize: func(context.Context, protocol.InitializeRequest) (protocol.InitializeResponse, error) {
			return protocol.InitializeResponse{ProtocolVersion: 1, AgentInfo: protocol.Some(protocol.Implementation{Name: "san", Version: version})}, nil
		},
		CreateSession: host.create,
		Prompt:        host.prompt,
	}, acp.AgentOptions{})
	if err != nil {
		return err
	}
	<-endpoint.Done()
	// Wait joins callbacks before any session writer is closed.
	err = endpoint.Wait(context.Background())
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	for _, s := range host.sessions {
		err = errors.Join(err, s.close())
	}
	return err
}

type acpHost struct {
	provider llm.Provider
	model    string
	mu       sync.Mutex
	sessions map[protocol.SessionID]*acpSession
}

// ACP does not apply shell directory handoffs to the session's CWD.
const acpBashCWDHelp = "Directory changes affect only this command; subsequent commands start in the original session working directory"

// Each SDK-admitted session has independent policy, transcript and history.
// The SDK serializes prompts for a session, including the response barrier.
type acpSession struct {
	data        agent.MainSession
	params      agent.BuildParams
	runtime     agent.Runtime
	hooks       *hook.Engine
	reminders   *reminder.Reminder
	lastPrompt  string
	sideEffects sync.Map
}

func (h *acpHost) create(ctx context.Context, req protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
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
	if len(req.MCPServers) != 0 {
		return protocol.NewSessionResponse{}, fmt.Errorf("ACP-supplied MCP servers are not supported")
	}
	settings, err := setting.LoadForCwd(req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	store, err := session.NewStore(req.CWD)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	opened, err := store.Create(session.CreateOptions{ID: session.NewSessionID(), Cwd: req.CWD, Model: h.model, MaxTokens: setting.DefaultMaxTokens})
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	s := &acpSession{data: agent.MainSession{Session: opened, Store: store}, reminders: reminder.New()}
	if err := s.data.Validate(); err != nil {
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	s.hooks = hook.NewEngine(settings, opened.ID(), req.CWD, opened.TranscriptPath())
	s.hooks.SetAuditCallback(opened.RecordHookAudit)
	s.hooks.SetLLMCompleter(buildHookCompleter(h.provider), h.model)
	audit := func(rec transcript.Record) { _ = opened.Append(context.Background(), rec) }
	policy := agent.PermissionPolicy{Settings: setting.New(settings), Session: setting.NewSessionPermissions(), Audit: audit}
	policy.Session.AddWorkingDirectory(req.CWD)
	if setting.New(settings).AutoMemory() {
		policy.AutoMemory = setting.MemoryStoresFor(req.CWD)
	}
	s.params = agent.BuildParams{SessionID: opened.ID(), CWD: req.CWD, Provider: h.provider, ModelID: h.model,
		MaxTokens:        setting.DefaultMaxTokens,
		FileObservations: fs.NewFileViews(), BashCWDHelp: acpBashCWDHelp,
		OnToolHookResponse: func(toolCallID string, response any) { s.sideEffects.Store(toolCallID, response) },
		PermissionRules: func(name string, args map[string]any) agent.PermDecisionResult {
			if acpBackgroundCall(name, args) {
				return agent.PermDecisionResult{Decision: perm.Reject, Reason: "ACP supports foreground commands only"}
			}
			return policy.Decide(name, args)
		},
		CanApproveByHook: func(name string, args map[string]any) bool {
			return !acpBackgroundCall(name, args) && policy.CanApproveByHook(name, args)
		},
		HookEngine: s.hooks, OnEvent: opened.RecordEvent,
		ResultFilter: agent.ResultPager{Store: store, SessionID: opened.ID()}.Filter,
		AutoMemory:   policy.AutoMemory, FileCache: filecache.New(),
	}
	// Tools requiring the TUI or process-global extension state are not offered.
	s.params.DisabledTools = setting.WithDefaultDisabledTools(settings.DisabledTools)
	for _, schema := range s.params.Schemas() {
		switch schema.Name {
		case "Read", "Write", "Edit", "Bash", "Glob", "Grep":
		default:
			s.params.DisabledTools[schema.Name] = true
		}
	}
	s.refreshMemory()
	outcome := s.hooks.Execute(ctx, hook.SessionStart, hook.HookInput{Source: "startup", Model: h.model})
	if outcome.AdditionalContext != "" {
		s.reminders.Enqueue(outcome.AdditionalContext)
	}
	if err := ctx.Err(); err != nil {
		s.hooks.Shutdown()
		_ = opened.Close()
		return protocol.NewSessionResponse{}, err
	}
	id := protocol.SessionID(opened.ID())
	h.mu.Lock()
	h.sessions[id] = s
	h.mu.Unlock()
	return protocol.NewSessionResponse{SessionID: id}, nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.hooks.Execute(ctx, hook.SessionEnd, hook.HookInput{Reason: "connection_closed"})
	s.hooks.Shutdown()
	return s.data.Session.Close()
}

func (s *acpSession) refreshMemory() {
	var user, project []string
	for _, file := range system.LoadMemoryFiles(s.params.CWD) {
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
	return s.data.Session.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched,
		State: &transcript.StatePatch{LastPrompt: new(s.lastPrompt), Mode: new("normal")}})
}

func acpBackgroundCall(name string, args map[string]any) bool {
	if name != "Bash" {
		return false
	}
	background, _ := args["run_in_background"].(bool)
	mode, _ := args["execution_mode"].(string)
	return background || (mode != "" && mode != "foreground")
}

func acpText(text string) protocol.ContentBlock {
	return protocol.ContentBlock{Value: protocol.ContentBlockTextVariant{Type: "text", Text: text}}
}
