package subagent

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestFallbackInboxStaysLiveAcrossHandoff(t *testing.T) {
	store, parent := continuationSessions(t, "fallback-owner")
	var workerID string
	child := namedHookProvider{continuationProvider{aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if err := broker.SendErr(broker.Message{To: workerID, Content: "during failure"}); err != nil {
			t.Errorf("mail rejected while the selected model failed: %v", err)
		}
		return aitest.Fails(errors.New("model_not_found"))(ctx, req)
	})}, "child-provider"}
	parentDriver := aitest.New(aitest.Says("fallback answer"))
	e := NewExecutor(namedHookProvider{continuationProvider{parentDriver}, "parent-provider"}, t.TempDir(), "parent-model", nil)
	e.SetResolver(&stubResolver{provider: child})
	bindContinuationSessions(t, e, store, parent)
	e.SetChildHooks(func(*session.Session, llm.Provider, string) (hook.Handler, func(llm.Provider, string), func()) {
		return childStartHooks{dispatch: func(hook.EventType) {}}, func(llm.Provider, string) {
			if err := broker.SendErr(broker.Message{To: workerID, Content: "during handoff"}); err != nil {
				t.Errorf("mail rejected between selected and fallback agents: %v", err)
			}
		}, func() {}
	})
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original task", Model: "openai/selected-model", OnIdentity: func(identity toolresult.AgentIdentity) {
		workerID = identity.ID
		if err := broker.SendErr(broker.Message{To: workerID, Content: "at startup"}); err != nil {
			t.Errorf("startup mail rejected: %v", err)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("fallback worker did not finish")
	}
	if info := worker.GetStatus(); info.Status != task.StatusCompleted {
		t.Fatalf("fallback worker failed: %+v", info)
	}
	want := []string{"original task", "at startup", "during failure", "during handoff"}
	var got []string
	for _, message := range parentDriver.Last().Messages {
		if message.Role == ai.RoleUser {
			got = append(got, message.Text())
		}
	}
	if len(got) != len(want) {
		t.Fatalf("fallback request user messages = %q, want %q", got, want)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Fatalf("fallback request user messages = %q, want %q", got, want)
		}
	}
	projection, err := store.Read(context.Background(), worker.GetStatus().AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, message := range projection.ActiveMessages() {
		if seen[message.ID] {
			t.Fatalf("native history duplicated message %s", message.ID)
		}
		seen[message.ID] = true
	}
	// Launch input precedes the boot catalogue; native replay must still
	// reproduce the request exactly instead of storing a standalone copy.
	var starts []*session.InferenceStartPayload
	for _, event := range projection.Events() {
		if start, ok := event.Payload.(*session.InferenceStartPayload); ok {
			starts = append(starts, start)
		}
	}
	if len(starts) == 0 || starts[len(starts)-1].Input.Conversation == nil {
		t.Fatalf("fallback inference was not reproducible from native history: %+v", starts)
	}
	if err := broker.SendErr(broker.Message{To: worker.GetID(), Content: "after completion"}); !errors.Is(err, broker.ErrNoRecipient) {
		t.Fatalf("completed worker retained its route: %v", err)
	}
}

func TestResumeRejectedBeforeAttemptReleasesInbox(t *testing.T) {
	store, parent := continuationSessions(t, "resume-owner")
	e := NewExecutor(continuationProvider{aitest.New(aitest.Hangs())}, t.TempDir(), "aitest", nil)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original objective"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if err := worker.Stop(); err != nil {
		t.Fatal(err)
	}
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("stopped worker did not settle")
	}
	before := worker.GetStatus()
	history, err := store.Read(context.Background(), before.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "continue"}); err == nil {
		t.Fatal("deliberately stopped worker resumed")
	}
	if after := worker.GetStatus(); after.Attempt != before.Attempt || after.Status != before.Status {
		t.Fatalf("rejected continuation changed lifecycle: before=%+v after=%+v", before, after)
	}
	if err := broker.SendErr(broker.Message{To: worker.GetID(), Content: "after rejection"}); !errors.Is(err, broker.ErrNoRecipient) {
		t.Fatalf("rejected continuation leaked its route: %v", err)
	}
	child, err := store.Open(context.Background(), before.AgentSessionID)
	if err != nil {
		t.Fatalf("rejected continuation leaked its writer claim: %v", err)
	}
	snapshot := child.Snapshot()
	if len(snapshot.ActiveMessages()) != len(history.ActiveMessages()) {
		t.Error("rejected continuation changed the conversation history")
	}
	_ = child.Close()
}
