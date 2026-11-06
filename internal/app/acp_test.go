package app

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type acpTestProvider struct{ client *ai.Client }

func (p acpTestProvider) Client(string, map[string]string) (*ai.Client, error) { return p.client, nil }
func (acpTestProvider) Name() string                                           { return "acp-test" }
func (acpTestProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "test", ContextWindow: 200000}}, nil
}

type acpFixture struct {
	host     *acpHost
	endpoint *acp.Agent
	client   *acp.Conn
	ctx      context.Context
	mu       sync.Mutex
	updates  []protocol.SessionNotification
	stopped  chan error
}

func newACPFixture(t testing.TB, driver *aitest.Driver, permission func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error), wrap ...func(io.WriteCloser) io.WriteCloser) *acpFixture {
	t.Helper()
	return newACPFixtureWithTimeout(t, driver, permission, acpWriteTimeout, wrap...)
}

func newACPFixtureWithTimeout(t testing.TB, driver *aitest.Driver, permission func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error), budget time.Duration, wrap ...func(io.WriteCloser) io.WriteCloser) *acpFixture {
	return newACPFixtureOptions(t, driver, permission, budget, protocol.InitializeRequest{ProtocolVersion: 1}, nil, wrap...)
}

func newACPFixtureOptions(t testing.TB, driver *aitest.Driver, permission func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error), budget time.Duration, init protocol.InitializeRequest, elicit func(context.Context, protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error), wrap ...func(io.WriteCloser) io.WriteCloser) *acpFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	// Connection lifetime must not consume individual requests' timeout budgets.
	ctx := t.Context()
	f := &acpFixture{ctx: ctx, host: &acpHost{provider: acpTestProvider{driver.Client()}, model: "test", sessions: make(map[protocol.SessionID]*acpSession)}}
	input, send := io.Pipe()
	recv, output := io.Pipe()
	var writer io.WriteCloser = output
	for _, wrap := range wrap {
		writer = wrap(writer)
	}
	var err error
	f.endpoint, err = f.host.start(ctx, input, writer, "test", budget)
	if err != nil {
		t.Fatal(err)
	}
	f.client, err = acp.New(ctx, recv, send, acp.Handlers{
		Request: func(ctx context.Context, c *acp.Conn, method string, params json.RawMessage) (any, error) {
			if method == "elicitation/create" {
				return acp.ElicitationHandler(elicit)(ctx, c, method, params)
			}
			var req protocol.RequestPermissionRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err
			}
			return permission(ctx, req)
		},
		Notification: func(_ context.Context, _ *acp.Conn, method string, params json.RawMessage) error {
			var update protocol.SessionNotification
			if err := json.Unmarshal(params, &update); err != nil {
				return err
			}
			f.mu.Lock()
			f.updates = append(f.updates, update)
			f.mu.Unlock()
			return nil
		},
	}, acp.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	f.stopped = make(chan error, 1)
	go func() { f.stopped <- f.host.wait(f.endpoint); close(f.stopped) }()
	t.Cleanup(func() {
		_ = f.client.Close()
		_ = f.endpoint.Close()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = f.client.Wait(wait)
		_ = f.endpoint.Wait(wait)
		if wait.Err() != nil {
			t.Error("ACP callbacks did not unwind")
			return
		}
		select {
		case <-f.stopped:
		case <-wait.Done():
			t.Error("ACP host did not close sessions")
			return
		}
		for _, s := range f.host.sessions {
			if s.runtime.Active() || len(s.runtime.Messages()) != 0 {
				t.Error("runtime retained after session close")
			}
		}
	})
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := f.client.Initialize(initCtx, init); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *acpFixture) session(t testing.TB, cwd string) protocol.SessionID {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	res, err := f.client.CreateSession(ctx, protocol.NewSessionRequest{CWD: cwd, MCPServers: []protocol.MCPServer{}})
	if err != nil {
		t.Fatal(err)
	}
	return res.SessionID
}
func (f *acpFixture) prompt(id protocol.SessionID, text string) (protocol.PromptResponse, error) {
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	return f.client.Prompt(ctx, protocol.PromptRequest{SessionID: id, Prompt: []protocol.ContentBlock{acpText(text)}})
}
func acpChoice(id string) protocol.RequestPermissionResponse {
	return protocol.RequestPermissionResponse{Outcome: protocol.RequestPermissionOutcome{Value: protocol.RequestPermissionOutcomeSelectedVariant{Outcome: "selected", OptionID: protocol.PermissionOptionID(id)}}}
}

func TestACPAutoCompaction(t *testing.T) {
	driver := aitest.New(
		aitest.Replies(ai.Response{Content: ai.TextContent("initial"), Usage: ai.Usage{Input: 500000}}),
		aitest.Says("compact summary"), aitest.Says("after compact"), aitest.Says("next"),
	)
	f := newACPFixture(t, driver, nil)
	id := f.session(t, t.TempDir())
	f.host.mu.Lock()
	s := f.host.sessions[id]
	f.host.mu.Unlock()
	compacted := make(chan core.Compacted, 1)
	record := s.params.OnEvent
	s.params.OnEvent = func(event core.Event) {
		record(event)
		if event, ok := event.(core.Compacted); ok {
			compacted <- event
		}
	}
	for _, prompt := range []string{"first", "second", "third"} {
		if _, err := f.prompt(id, prompt); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case event := <-compacted:
		if event.Trigger != "auto" {
			t.Fatalf("trigger = %q", event.Trigger)
		}
	default:
		t.Fatal("automatic compaction did not run")
	}
	if driver.Calls() != 4 {
		t.Fatalf("model calls = %d", driver.Calls())
	}
	if !strings.Contains(s.runtime.Messages()[0].Text(), "compact summary") {
		t.Fatal("compacted history not retained")
	}
}

func TestACPCancelInference(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	wait := func(ctx context.Context, _ *ai.Request) iter.Seq2[ai.Delta, error] {
		return func(yield func(ai.Delta, error) bool) {
			close(entered)
			<-ctx.Done()
			close(exited)
			yield(ai.Delta{}, ctx.Err())
		}
	}
	f := newACPFixture(t, aitest.New(wait, aitest.Says("successor")), nil)
	id := f.session(t, t.TempDir())
	done := make(chan error, 1)
	go func() {
		res, err := f.prompt(id, "wait")
		if err == nil && res.StopReason != protocol.StopReasonCancelled {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("inference never started")
	}
	if err := f.client.CancelPrompt(f.ctx, protocol.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-f.ctx.Done():
		t.Fatal("cancel did not return")
	}
	select {
	case <-exited:
	default:
		t.Fatal("response preceded inference unwind")
	}
	f.host.mu.Lock()
	s := f.host.sessions[id]
	f.host.mu.Unlock()
	gate := s.runtime.PermissionGate()
	if gate == nil {
		t.Fatal("cancel discarded session runtime")
	}
	if _, err := f.prompt(id, "next"); err != nil {
		t.Fatal(err)
	}
	if s.runtime.PermissionGate() != gate {
		t.Fatal("next prompt rebuilt session runtime")
	}
}

func TestACPHooksAndRecordingBarrier(t *testing.T) {
	for _, failRecord := range []bool{false, true} {
		t.Run(map[bool]string{true: "recording failure", false: "hook continuation"}[failRecord], func(t *testing.T) {
			driver := aitest.New(aitest.Says("initial"), aitest.Says("continued"))
			f := newACPFixture(t, driver, nil)
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("workspace instruction"), 0o600); err != nil {
				t.Fatal(err)
			}
			id := f.session(t, cwd)
			f.host.mu.Lock()
			s := f.host.sessions[id]
			f.host.mu.Unlock()
			if failRecord {
				// Fail recording after admission validation, before execution writes.
				s.hooks.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
					return hook.HookOutput{}, s.data.Session.Close()
				}})
				if _, err := f.prompt(id, "hello"); err == nil {
					t.Fatal("successful response hid essential recording failure")
				}
				if s.runtime.Active() {
					t.Fatal("failed turn left runtime active")
				}
				return
			}
			s.hooks.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
				return hook.HookOutput{HookSpecificOutput: &hook.HookSpecificOutput{AdditionalContext: "prompt hook instruction"}}, nil
			}})
			stops := 0
			s.hooks.AddSessionFunctionHook(hook.Stop, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
				stops++
				if stops == 1 {
					return hook.HookOutput{Continue: new(false), Reason: "check again"}, nil
				}
				return hook.HookOutput{}, nil
			}})
			if _, err := f.prompt(id, "hello"); err != nil {
				t.Fatal(err)
			}
			if stops != 2 || driver.Calls() != 2 {
				t.Fatalf("stop checks/model calls = %d/%d", stops, driver.Calls())
			}
			data, _ := json.Marshal(driver.Sent()[0])
			if !strings.Contains(string(data), "workspace instruction") || !strings.Contains(string(data), "prompt hook instruction") {
				t.Fatal("native preparation missing instructions")
			}
			history, err := s.data.Store.Read(string(id))
			if err != nil {
				t.Fatal(err)
			}
			if len(history.Messages) == 0 {
				t.Fatal("successful protocol response preceded native persistence")
			}
		})
	}
}

func TestACPNativeDenyAndUnsupportedInput(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.local.json"), []byte(`{"permissions":{"deny":["Write(**)"]},"disabledTools":{"Read":true,"TaskOutput":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cwd, "forbidden")
	input, _ := json.Marshal(map[string]string{"file_path": path, "content": "no"})
	f := newACPFixture(t, aitest.New(aitest.Asks(ai.ToolCall{ID: "denied", Name: "Write", Input: string(input)}), aitest.Says("denied")), func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		t.Error("native deny escalated to client")
		return acpChoice("allow_once"), nil
	})
	id := f.session(t, cwd)
	f.host.mu.Lock()
	hooks := f.host.sessions[id].hooks
	f.host.mu.Unlock()
	hooks.AddSessionFunctionHook(hook.PreToolUse, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
		return hook.HookOutput{HookSpecificOutput: &hook.HookSpecificOutput{PermissionDecision: "allow"}}, nil
	}})
	if _, err := f.prompt(id, "try"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("native deny bypassed")
	}
	f.host.mu.Lock()
	s := f.host.sessions[id]
	f.host.mu.Unlock()
	for _, schema := range s.params.ToolSchemas() {
		if schema.Name == "Read" || schema.Name == "TaskOutput" {
			t.Error("disabled tool exposed")
		}
	}
	if _, err := f.prompt(id, ""); err == nil {
		t.Error("empty prompt accepted")
	}
	if _, err := f.client.CreateSession(f.ctx, protocol.NewSessionRequest{CWD: "relative", MCPServers: []protocol.MCPServer{}}); err == nil {
		t.Error("relative cwd accepted")
	}
	if s.params.CanApproveByHook("Write", map[string]any{"file_path": path, "content": "no"}) {
		t.Error("hook can bypass native deny")
	}
}

func TestACPToolPermissionAndConsecutiveTurns(t *testing.T) {
	for _, choice := range []string{"allow_once", "reject_once", "not-offered"} {
		allow := choice == "allow_once"
		t.Run(choice, func(t *testing.T) {
			cwd := t.TempDir()
			path := filepath.Join(cwd, "answer.txt")
			input, _ := json.Marshal(map[string]string{"file_path": path, "content": "approved"})
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "write-1", Name: "Write", Input: string(input)}), aitest.Says("finished"), aitest.Says("next"), aitest.Says("separate"))
			var requests int
			f := newACPFixture(t, driver, func(_ context.Context, req protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				requests++
				if req.ToolCall.ToolCallID != "write-1" || len(req.Options) != 2 {
					t.Errorf("permission = %+v", req)
				}
				return acpChoice(choice), nil
			})
			id := f.session(t, cwd)
			res, err := f.prompt(id, "write it")
			if err != nil || res.StopReason != protocol.StopReasonEndTurn {
				t.Fatalf("prompt: %+v %v", res, err)
			}
			if requests != 1 {
				t.Fatalf("permissions = %d", requests)
			}
			_, statErr := os.Stat(path)
			if allow && statErr != nil {
				t.Fatal(statErr)
			}
			if !allow && !os.IsNotExist(statErr) {
				t.Fatal("denied tool executed")
			}
			if _, err := f.prompt(id, "again"); err != nil {
				t.Fatal(err)
			}
			other := f.session(t, t.TempDir())
			if _, err := f.prompt(other, "new session"); err != nil {
				t.Fatal(err)
			}
			f.host.mu.Lock()
			first, second := f.host.sessions[id], f.host.sessions[other]
			f.host.mu.Unlock()
			if !first.runtime.Active() || !second.runtime.Active() {
				t.Fatal("session runtime not retained between prompts")
			}
			if len(first.runtime.Messages()) <= len(second.runtime.Messages()) {
				t.Fatal("conversation not retained or isolated")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			var text, start, end bool
			for _, u := range f.updates {
				if u.SessionID != id {
					continue
				}
				switch v := u.Update.Value.(type) {
				case protocol.SessionUpdateAgentMessageChunkVariant:
					text = true
				case protocol.SessionUpdateToolCallVariant:
					start = true
				case protocol.SessionUpdateToolCallUpdateVariant:
					if v.Status.Value == protocol.ToolCallStatusCompleted || v.Status.Value == protocol.ToolCallStatusFailed {
						end = true
					}
				}
			}
			if !text || !start || !end {
				t.Fatalf("stream text/start/end = %v/%v/%v", text, start, end)
			}
		})
	}
}

func TestACPCancelPermissionAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{true: "disconnect", false: "cancel"}[disconnect], func(t *testing.T) {
			cwd := t.TempDir()
			path := filepath.Join(cwd, "never")
			input, _ := json.Marshal(map[string]string{"file_path": path, "content": "no"})
			nextPath := filepath.Join(cwd, "approved-next")
			nextInput, _ := json.Marshal(map[string]string{"file_path": nextPath, "content": "yes"})
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "wait", Name: "Write", Input: string(input)}),
				aitest.Asks(ai.ToolCall{ID: "next", Name: "Write", Input: string(nextInput)}), aitest.Says("after cancel"))
			waiting := make(chan struct{})
			f := newACPFixture(t, driver, func(ctx context.Context, req protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				if req.ToolCall.ToolCallID == "next" {
					return acpChoice("allow_once"), nil
				}
				close(waiting)
				<-ctx.Done()
				return protocol.RequestPermissionResponse{}, ctx.Err()
			})
			id := f.session(t, cwd)
			done := make(chan error, 1)
			go func() {
				res, err := f.prompt(id, "wait")
				if !disconnect && err == nil && res.StopReason != protocol.StopReasonCancelled {
					err = io.ErrUnexpectedEOF
				}
				done <- err
			}()
			select {
			case <-waiting:
			case <-f.ctx.Done():
				t.Fatal("permission not requested")
			}
			if disconnect {
				_ = f.client.Close()
			} else if err := f.client.CancelPrompt(f.ctx, protocol.CancelNotification{SessionID: id}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if !disconnect && err != nil {
					t.Fatal(err)
				}
			case <-f.ctx.Done():
				t.Fatal("prompt did not unwind")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("cancelled permission executed tool")
			}
			if !disconnect {
				if _, err := f.prompt(id, "continue"); err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(nextPath); err != nil || string(data) != "yes" {
					t.Fatalf("successor permission/tool failed: %q, %v", data, err)
				}
			}
		})
	}
}
