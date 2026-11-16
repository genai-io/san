package subagent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestInboxCapacityRecoveryDeliversRetainedCompletion(t *testing.T) {
	store, parent := continuationSessions(t, "capacity-owner")
	driver := aitest.New(aitest.Says("first answer"), aitest.Says("handled completion"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work", MaxSteps: 2, OnIdentity: func(identity toolresult.AgentIdentity) {
		for i := range maxPendingWorkerMessages {
			if !broker.Send(broker.Message{To: identity.ID, Content: fmt.Sprintf("startup %d", i)}) {
				t.Errorf("startup message %d rejected", i)
			}
		}
		if !broker.SendCompletion(broker.Message{To: identity.ID, SessionID: parent.ID(), Content: "retained child completion", TaskCompletion: &broker.TaskCompletion{TaskID: task.NewID(), Attempt: 1}}) {
			t.Error("completion was not retained")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("worker did not finish")
	}
	if info := worker.GetStatus(); info.Status != task.StatusCompleted || driver.Calls() < 1 || driver.Calls() > 2 {
		t.Fatalf("capacity recovery failed: %+v calls=%d", info, driver.Calls())
	}
	count := 0
	for _, message := range driver.Last().Messages {
		if message.Text() == "retained child completion" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("retained completion delivered %d times, want 1", count)
	}
}

func TestInvalidContinuationSourceDoesNotPoisonHistory(t *testing.T) {
	store, parent := continuationSessions(t, "input-owner")
	driver := aitest.New(aitest.Says("first"), aitest.Says("continued"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("initial worker hung")
	}
	before := worker.GetStatus()
	history, err := store.Read(context.Background(), before.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "invalid", SourceSession: parent.ID(), SourceSeq: 99999}); err == nil {
		t.Fatal("invalid source accepted")
	}
	after, err := store.Read(context.Background(), before.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.ActiveMessages()) != len(after.ActiveMessages()) || worker.GetStatus().Attempt != before.Attempt {
		t.Fatal("rejected input changed history or attempt count")
	}
	if _, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "valid follow-up"}); err != nil {
		t.Fatalf("rejected source poisoned future continuation: %v", err)
	}
	if !worker.WaitForCompletion(5*time.Second) || worker.GetStatus().Status != task.StatusCompleted || driver.Calls() != 2 {
		t.Fatal("valid continuation failed after rejected source")
	}
}
