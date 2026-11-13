package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/san/tests/integration/testutil"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func mainSessionFixture(t *testing.T) MainSession {
	t.Helper()
	store := testutil.OpenSessionStore(t, t.TempDir())
	rec, err := store.Create(context.Background(), session.CreateOptions{AgentID: "main", Config: &session.EventConfig{Model: new("fake")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rec.Close() })
	return MainSession{Session: rec, Store: store}
}

func TestMainSessionRequiresOpenedOwner(t *testing.T) {
	mainSession := mainSessionFixture(t)
	for _, change := range []func(*MainSession){
		func(r *MainSession) { r.Session = nil },
		func(r *MainSession) { r.Store = nil },
		func(r *MainSession) { r.Store = mainSessionFixture(t).Store },
	} {
		invalid := mainSession
		change(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatal("accepted incomplete main session")
		}
	}
	if err := mainSession.Session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mainSession.Validate(); err == nil {
		t.Fatal("accepted closed recorder")
	}
}

// No TUI, replacement builder, or substitute gate: run the production runtime
// through a streamed tool turn and read the native transcript at its boundary.
func TestMainSessionRuntimePermissionPagingAndRecording(t *testing.T) {
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			mainSession := mainSessionFixture(t)
			data := setting.NewData()
			const toolName = "mcp__fixture__output"
			if allow {
				data.Permissions.Allow = []string{toolName}
			} else {
				data.Permissions.Deny = []string{toolName}
			}
			hooks := hook.NewEngine(data, mainSession.Session.ID(), t.TempDir(), mainSession.Session.Path())
			hooks.SetCompletionRecorder(session.NewRecorder(mainSession.Session, "fake").RecordHook)
			hooks.AddSessionFunctionHook(hook.PreToolUse, toolName, hook.FunctionHook{
				Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
					return hook.HookOutput{}, nil
				},
			})
			var calls atomic.Int32
			full := strings.Repeat("界", 40_000)
			fixture := sdkagent.ToolFunc(toolName, "return output", func(context.Context, struct{}) (sdkagent.Result, error) {
				calls.Add(1)
				return sdkagent.Result{Content: ai.TextContent(full), Details: toolresult.BashDetails{LineCount: 42, Tail: "raw tail"}}, nil
			})
			driver := aitest.New(
				aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "call-1", Name: toolName, Input: `{}`})}, StopReason: ai.StopToolUse}),
				aitest.Says("finished"),
			)
			if err := mainSession.Validate(); err != nil {
				t.Fatal(err)
			}
			policy := PermissionPolicy{Settings: data, Session: setting.NewSessionPermissions()}
			params := BuildParams{Provider: runOnceProvider{driver}, ModelID: "fake", CWD: t.TempDir(), MCPTools: []core.Tool{fixture},
				SessionID: mainSession.Session.ID(), Recording: mainSession.Session,
				HookEngine: hooks, PermissionRules: policy.Decide, CanApproveByHook: policy.CanApproveByHook,
				ResultFilter: ResultPager{Store: mainSession.Store, SessionID: mainSession.Session.ID()}.Filter,
			}
			runtime := &Runtime{}
			if err := runtime.Start(params, nil); err != nil {
				t.Fatal(err)
			}
			defer stopRuntime(t, runtime)
			if err := runtime.Send(core.UserMessage("run fixture", nil)); err != nil {
				t.Fatal(err)
			}
			timeout := time.NewTimer(5 * time.Second)
			defer timeout.Stop()
			for {
				select {
				case ev := <-runtime.Outbox():
					if _, ok := ev.(core.TurnEnded); !ok {
						continue
					}
					if err := mainSession.Session.Err(); err != nil {
						t.Fatal(err)
					}
					history, err := mainSession.Store.Read(context.Background(), mainSession.Session.ID())
					if err != nil {
						t.Fatal(err)
					}
					if len(history.Messages()) < 4 {
						t.Fatalf("terminal event preceded transcript writes: %d messages", len(history.Messages()))
					}
					bytes, err := os.ReadFile(mainSession.Session.Path())
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(bytes), "permission.decide") {
						t.Fatal("missing permission audit")
					}
					if !strings.Contains(string(bytes), "hook.finish") {
						t.Fatal("missing hook audit")
					}
					blob := filepath.Join(filepath.Dir(mainSession.Session.Path()), "blobs", "tool-result", "call-1")
					if allow {
						if calls.Load() != 1 {
							t.Fatalf("executions = %d", calls.Load())
						}
						if !strings.Contains(string(bytes), "Full output persisted") {
							t.Fatal("paged result missing from native transcript")
						}
						content, err := os.ReadFile(blob)
						if err != nil {
							t.Fatal(err)
						}
						if string(content) != full {
							t.Fatal("full output not retained")
						}
						found := false
						for _, row := range mainSession.Store.ChatRows(history) {
							if row.ToolResult != nil && row.ToolResult.ToolCallID == "call-1" {
								found = true
								details, ok := row.ToolDetails.(toolresult.BashDetails)
								if row.ToolResult.Text() != full || !ok || details.LineCount != 42 || details.Tail != "raw tail" {
									t.Fatalf("hydrated row=%+v", row)
								}
							}
						}
						if !found {
							t.Fatal("missing tool display")
						}
					} else if calls.Load() != 0 {
						t.Fatal("denied tool ran")
					}
					return
				case <-timeout.C:
					t.Fatal("runtime did not finish")
				}
			}
		})
	}
}

func TestMainSessionPagingPrecedesLiveRecallAndKeepsOwner(t *testing.T) {
	mainSession := mainSessionFixture(t)
	memoryPath := filepath.Join(t.TempDir(), "memory.md")
	if err := os.WriteFile(memoryPath, []byte("remember-this-fact"), 0600); err != nil {
		t.Fatal(err)
	}
	pick := &Recall{ready: make(chan struct{}), memories: []system.Memory{{Path: memoryPath}}}
	close(pick.ready)
	var current atomic.Pointer[Recall]
	if err := mainSession.Validate(); err != nil {
		t.Fatal(err)
	}
	params := BuildParams{ResultFilter: WithRecall(
		ResultPager{Store: mainSession.Store, SessionID: mainSession.Session.ID()}.Filter, current.Load,
	)}
	owner := mainSession.Session.ID()
	mainSession.Session = mainSessionFixture(t).Session
	current.Store(pick)
	out, err := params.ResultFilter(context.Background(), sdkagent.PostToolContext{
		Call: ai.ToolCall{ID: "call-recall"}, Result: sdkagent.TextResult(strings.Repeat("x", 100_001)),
	})
	if err != nil || out == nil {
		t.Fatalf("filter: %v, %v", out, err)
	}
	text := out.Content.Text()
	if !strings.Contains(text, "blobs/tool-result/"+owner+"/call-recall") || !strings.Contains(text, "remember-this-fact") {
		t.Fatalf("lost owner or recall: %s", text[10_000:])
	}
	out, err = params.ResultFilter(context.Background(), sdkagent.PostToolContext{Result: sdkagent.TextResult("small")})
	if err != nil || out != nil {
		t.Fatal("recall repeated or small result rewritten")
	}
}

func TestMainSessionRuntimeCancelRetiresHumanApproval(t *testing.T) {
	mainSession := mainSessionFixture(t)
	const name = "mcp__fixture__approval"
	data := setting.NewData()
	data.Permissions.Ask = []string{name}
	var calls atomic.Int32
	fixture := sdkagent.ToolFunc(name, "needs approval", func(context.Context, struct{}) (sdkagent.Result, error) {
		calls.Add(1)
		return sdkagent.TextResult("must not run"), nil
	})
	driver := aitest.New(aitest.Replies(ai.Response{
		Content:    ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "cancel-call", Name: name, Input: `{}`})},
		StopReason: ai.StopToolUse,
	}))
	if err := mainSession.Validate(); err != nil {
		t.Fatal(err)
	}
	policy := PermissionPolicy{Settings: data, Session: setting.NewSessionPermissions()}
	params := BuildParams{Provider: runOnceProvider{driver}, ModelID: "fake", MCPTools: []core.Tool{fixture},
		SessionID: mainSession.Session.ID(), Recording: mainSession.Session,
		PermissionRules: policy.Decide, CanApproveByHook: policy.CanApproveByHook,
	}
	runtime := &Runtime{}
	if err := runtime.Start(params, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, runtime)
	gate := runtime.PermissionGate()
	defer gate.Close()
	requests := make(chan *PermGateRequest, 1)
	go func() { request, _ := gate.Recv(); requests <- request }()
	if err := runtime.Send(core.UserMessage("request approval", nil)); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var request *PermGateRequest
	for request == nil {
		select {
		case request = <-requests:
		case <-runtime.Outbox():
		case <-deadline.C:
			t.Fatal("no human approval request")
		}
	}
	if request.ToolCallID != "cancel-call" {
		t.Fatalf("lost tool identity: %q", request.ToolCallID)
	}
	receipt := runtime.Cancel()
	if receipt == nil {
		t.Fatal("missing cancellation receipt")
	}
	select {
	case <-request.Context.Done():
	case <-deadline.C:
		t.Fatal("approval remained live")
	}
	request.Response <- PermGateResponse{Allow: true, Reason: "late response"}
	for {
		select {
		case <-runtime.Outbox():
		case <-receipt.ExecutionDone():
			if calls.Load() != 0 {
				t.Fatal("late approval executed tool")
			}
			if err := mainSession.Session.Err(); err != nil {
				t.Fatal(err)
			}
			return
		case <-deadline.C:
			t.Fatal("cancelled execution did not unwind")
		}
	}
}

func TestMainSessionCancelledJudgeCannotGrant(t *testing.T) {
	mainSession := mainSessionFixture(t)
	policy := PermissionPolicy{Settings: setting.NewData(), Session: &setting.SessionPermissions{Mode: setting.ModeAutoPilot}}
	entered := make(chan struct{})
	release := make(chan struct{})
	snapshot := func() JudgeSnapshot {
		return JudgeSnapshot{Engaged: true, Enabled: true,
			Judge: func(context.Context, judge.Request) (judge.Verdict, error) {
				close(entered)
				<-release
				return judge.Verdict{Allow: true, Reason: "late"}, nil
			},
		}
	}
	if err := mainSession.Validate(); err != nil {
		t.Fatal(err)
	}
	params := BuildParams{Provider: stubProvider{}, ModelID: "fake", PermissionRules: policy.Decide,
		CanApproveByHook: policy.CanApproveByHook,
		PermissionJudge: func(ctx context.Context, name string, args map[string]any, reason string) PermJudgeResult {
			return policy.Judge(ctx, name, args, reason, snapshot())
		},
	}
	_, gate, err := buildAgent(params)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan bool, 1)
	args := map[string]any{"command": "go test ./..."}
	go func() { allowed, _ := gate.Check(ctx, "Bash", args, false, ""); done <- allowed }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("review did not start")
	}
	cancel()
	close(release)
	select {
	case allowed := <-done:
		if allowed {
			t.Fatal("cancelled review allowed execution")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not release gate")
	}
	if d := params.PermissionRules("Bash", args); !d.Judgeable {
		t.Fatalf("late approval changed session policy: %+v", d)
	}
	bytes, err := os.ReadFile(mainSession.Session.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "permission.decide") {
		t.Fatal("late verdict recorded as approval")
	}
}
