package app

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestManualCompactionCancellationJoinsPreCompactModelHook(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m, owner := recordedCompactModel(t, func(ctx context.Context, _ *ai.Request) iter.Seq2[ai.Delta, error] {
		return func(yield func(ai.Delta, error) bool) {
			close(entered)
			<-ctx.Done()
			<-release
			yield(ai.Delta{}, ctx.Err())
		}
	})
	settings := setting.NewData()
	settings.Hooks["PreCompact"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "verify"}}}}
	engine := hook.NewEngine(settings, owner.ID(), m.env.CWD, owner.Path())
	bindSessionHooks(engine, m.env.LLMProvider, owner, "m")
	m.services.Hook = engine
	t.Cleanup(engine.Shutdown)
	req := m.BuildCompactRequest("", "manual")
	runDone := make(chan struct{})
	go func() { defer close(runDone); req.Run() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pre-compact model hook did not start")
	}
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- m.cancelManualCompaction() }()
	<-req.Ctx.Done()
	select {
	case err := <-cancelDone:
		t.Fatalf("retired writer before hook returned: %v", err)
	default:
	}
	close(release)
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	<-runDone // no UI result processing is required to retire the owner
	p := owner.Snapshot()
	var hookFinished, inferenceFinished bool
	for _, event := range p.Events() {
		hookFinished = hookFinished || event.Type == session.EventHookFinish
		inferenceFinished = inferenceFinished || event.Type == session.EventInferenceFinish
	}
	if !hookFinished || !inferenceFinished || len(p.Unfinished().Inferences) != 0 {
		t.Fatal("retirement lost native hook/inference finish")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManualCompactionCancellationFencesUnstartedHook(t *testing.T) {
	m, owner := recordedCompactModel(t, aitest.Says("unused"))
	settings := setting.NewData()
	engine := hook.NewEngine(settings, owner.ID(), m.env.CWD, owner.Path())
	called := false
	engine.AddSessionFunctionHook(hook.PreCompact, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
		called = true
		return hook.HookOutput{}, nil
	}})
	m.services.Hook = engine
	t.Cleanup(engine.Shutdown)
	req := m.BuildCompactRequest("", "manual")
	if err := m.cancelManualCompaction(); err != nil {
		t.Fatal(err)
	}
	outcome := req.PreCompact(context.Background())
	if called || outcome.Error == nil {
		t.Fatal("retired command executed prepared hook")
	}
}

func TestManualCompactionCancellationJoinsTerminalWrite(t *testing.T) {
	m, owner := recordedCompactModel(t, aitest.Says("unused"))
	req := m.BuildCompactRequest("", "manual")
	finish, err := req.Completion.Admission()(t.Context(), "test", ai.Model{ID: "m"}, &ai.Request{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.cancelManualCompaction() }()
	<-req.Ctx.Done()
	select {
	case err := <-done:
		t.Fatalf("cancel returned before provider terminal callback: %v", err)
	default:
	}
	if err := finish(context.Background(), &ai.Response{Usage: ai.Usage{Input: 13}}, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	if len(p.Unfinished().Inferences) != 0 {
		t.Fatal("cancel joined without recording terminal inference")
	}
	if m.pendingCompaction != nil {
		t.Fatal("completed retirement remains pending")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
