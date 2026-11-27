package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/runtime/mail"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	agenttools "github.com/genai-io/san/internal/tool/agent"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type handoffWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *handoffWaitContext) Deadline() (time.Time, bool) {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Deadline()
}

func TestWorkerInboxSealsUntilTaskRetires(t *testing.T) {
	store, parent := continuationSessions(t, "closing-owner")
	pool := task.NewManager()
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("done"))}, t.TempDir(), "aitest", nil)
	e.ConfigureOwner(pool, Default(), nil, nil)
	bindContinuationSessions(t, e, store, parent)
	cleaning, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var child *session.Session
	e.SetChildHooks(func(owner *session.Session, _ llm.Provider, _ string) (hook.Handler, func(llm.Provider, string), func()) {
		child = owner
		return nil, nil, func() { close(cleaning); <-release }
	})
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = worker.Stop(); pool.Join() })
	select {
	case <-cleaning:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach cleanup")
	}
	if !worker.IsRunning() {
		t.Fatal("cleanup barrier no longer holds the running execution")
	}
	before := child.Snapshot()
	if err := mail.SendErr(mail.Message{To: worker.GetID(), Content: "late instruction"}); !errors.Is(err, mail.ErrInboxClosing) {
		t.Fatalf("running worker lost its closing route: %v", err)
	}
	after := child.Snapshot()
	if len(before.Events()) != len(after.Events()) {
		t.Fatal("closing rejection persisted unsolicited input")
	}
	unblock()
	pool.Join()
	if err := mail.SendErr(mail.Message{To: worker.GetID()}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatalf("retired worker retained its route: %v", err)
	}
}

func TestSendMessageContinuesAfterWorkerCleanup(t *testing.T) {
	store, parent := continuationSessions(t, "handoff-owner")
	driver := aitest.New(aitest.Says("first answer"), aitest.Says("follow-up answer"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	e.ConfigureOwner(task.Default(), Default(), nil, nil)
	bindContinuationSessions(t, e, store, parent)
	cleaning, release := make(chan struct{}), make(chan struct{})
	var cleanupOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	e.SetChildHooks(func(*session.Session, llm.Provider, string) (hook.Handler, func(llm.Provider, string), func()) {
		return nil, nil, func() { cleanupOnce.Do(func() { close(cleaning); <-release }) }
	})
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original task"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = worker.Stop(); task.Default().Join(); task.Default().Remove(worker.GetID()) })
	select {
	case <-cleaning:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach cleanup")
	}
	if err := mail.SendErr(mail.Message{To: worker.GetID()}); !errors.Is(err, mail.ErrInboxClosing) {
		t.Fatalf("cleanup route = %v, want closing", err)
	}
	sender := agenttools.NewSendMessageTool()
	sender.SetExecutor(NewExecutorAdapter(e))
	var approvals atomic.Int32
	adapted := tool.AdaptTool(sender, sender.Schema(), func() string { return "." }, tool.WithPermissionCheck(perm.PermissionFunc(func(_ context.Context, name string, _ map[string]any) (bool, string) {
		if name == tool.ToolAgent {
			approvals.Add(1)
		}
		return true, ""
	})))
	input, _ := json.Marshal(map[string]string{"to": worker.GetID(), "message": "verify the original findings"})
	result := make(chan error, 1)
	waitCtx := &handoffWaitContext{Context: task.WithSessionID(t.Context(), parent.ID()), waiting: make(chan struct{})}
	go func() {
		_, err := adapted.Run(waitCtx, ai.ToolCall{Name: tool.ToolSendMessage, Input: string(input)})
		result <- err
	}()
	select {
	case <-waitCtx.waiting:
	case err := <-result:
		t.Fatalf("sender returned before entering closing wait: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("sender did not enter closing wait")
	}
	if !worker.IsRunning() || approvals.Load() != 0 {
		t.Fatal("sender bypassed the running cleanup barrier")
	}
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("message did not cross cleanup boundary")
	}
	task.Default().Join()
	info := worker.GetStatus()
	if info.Status != task.StatusCompleted || info.Attempt != 2 || driver.Calls() != 2 || approvals.Load() != 1 {
		t.Fatalf("wrong handoff: task=%+v calls=%d approvals=%d", info, driver.Calls(), approvals.Load())
	}
	history, err := store.Read(t.Context(), info.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	followups := 0
	for _, message := range history.Messages() {
		if strings.Contains(message.Text(), "verify the original findings") {
			followups++
		}
	}
	if followups != 1 {
		t.Fatalf("follow-up persisted %d times, want 1", followups)
	}
}

type replacementRouteObserver struct {
	completed func(task.TaskInfo)
}

func (*replacementRouteObserver) TaskCreated(task.TaskInfo) {}
func (o *replacementRouteObserver) TaskCompleted(info task.TaskInfo) {
	o.completed(info)
}

func TestCompletedWorkerCleanupPreservesReplacementRoute(t *testing.T) {
	store, parent := continuationSessions(t, "replacement-owner")
	pool := task.NewManager()
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("done"))}, t.TempDir(), "aitest", nil)
	e.ConfigureOwner(pool, Default(), nil, nil)
	bindContinuationSessions(t, e, store, parent)
	var unregister func()
	calls := 0
	task.SetLifecycleHandler(&replacementRouteObserver{completed: func(info task.TaskInfo) {
		unregister = mail.RegisterAccept(info.ID, func(mail.Message) error {
			calls++
			return nil
		})
	}})
	t.Cleanup(func() { task.SetLifecycleHandler(nil) })
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(); pool.Join() })
	pool.Join()
	if unregister == nil {
		t.Fatal("completion did not install the replacement route")
	}
	defer unregister()
	if err := mail.SendErr(mail.Message{To: worker.GetID()}); err != nil || calls != 1 {
		t.Fatalf("old cleanup removed replacement route: err=%v calls=%d", err, calls)
	}
}

type unencodableCatalogueTool struct{ tool.Tool }

func (t unencodableCatalogueTool) Schema() core.ToolSchema {
	schema := t.Tool.Schema()
	schema.Definition = map[string]any{"type": "object", "invalid": make(chan struct{})}
	return schema
}

func TestWorkerBuildFailureSealsBeforeCleanup(t *testing.T) {
	original, ok := tool.Get("Read")
	if !ok {
		t.Fatal("Read fixture tool is not registered")
	}
	tool.Register(unencodableCatalogueTool{original})
	t.Cleanup(func() { tool.Register(original) })

	store, parent := continuationSessions(t, "build-failure-owner")
	pool := task.NewManager()
	driver := aitest.New(aitest.Says("must not run"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	e.ConfigureOwner(pool, Default(), nil, nil)
	bindContinuationSessions(t, e, store, parent)
	cleaning, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var child *session.Session
	e.SetChildHooks(func(owner *session.Session, _ llm.Provider, _ string) (hook.Handler, func(llm.Provider, string), func()) {
		child = owner
		return nil, nil, func() { close(cleaning); <-release }
	})
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); _ = worker.Stop(); pool.Join() })
	select {
	case <-cleaning:
	case <-time.After(5 * time.Second):
		t.Fatal("failed build did not reach cleanup")
	}
	if !worker.IsRunning() || driver.Calls() != 0 {
		t.Fatal("fixture did not stop before inference in running cleanup")
	}
	before := child.Snapshot()
	err = mail.SendErr(mail.Message{To: worker.GetID(), Content: "must not persist after failed build"})
	if !errors.Is(err, mail.ErrRejected) || errors.Is(err, mail.ErrInboxClosing) {
		t.Fatalf("failed-build cleanup rejection = %v, want generic rejection", err)
	}
	after := child.Snapshot()
	if len(after.Events()) != len(before.Events()) {
		t.Fatal("failed-build cleanup accepted unsolicited input")
	}
	unblock()
	pool.Join()
	info := worker.GetStatus()
	if info.Status != task.StatusFailed || !strings.Contains(info.Error, "unsupported type: chan struct {}") {
		t.Fatalf("fixture did not trigger catalogue build failure: %+v", info)
	}
	if err := mail.SendErr(mail.Message{To: worker.GetID()}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatalf("failed worker retained its route: %v", err)
	}
}
