package subagent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestPreparedInboxPreservesWorkerToolIdentity(t *testing.T) {
	store, parent := continuationSessions(t, "context-owner")
	var workerID string
	var mu sync.Mutex
	var received []broker.Message
	cleanup := broker.Register(broker.Main, func(message broker.Message) bool {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, message)
		return true
	})
	defer cleanup()
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		return aitest.Asks(
			ai.ToolCall{ID: "to-main", Name: tool.ToolSendMessage, Input: `{"to":"main","message":"actionable finding"}`},
			ai.ToolCall{ID: "to-self", Name: tool.ToolSendMessage, Input: fmt.Sprintf(`{"to":%q,"message":"must reject self"}`, workerID)},
		)(ctx, req)
	}, aitest.Says("finished"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	e.SetParentPermissionMode(func() PermissionMode { return PermissionBypass })
	e.SetDisabledTools(map[string]bool{tool.ToolSendMessage: false})
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "send a finding", OnIdentity: func(identity toolresult.AgentIdentity) {
		workerID = identity.ID
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("worker did not finish")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0].From != workerID || received[0].SessionID != parent.ID() {
		t.Fatalf("outbound worker identity was lost: %+v", received)
	}
	selfRejected := false
	for _, message := range driver.Last().Messages {
		for _, result := range message.ToolResults() {
			if result.ToolCallID == "to-self" {
				selfRejected = strings.Contains(fmt.Sprint(result), "cannot send a message to yourself")
			}
		}
	}
	if !selfRejected {
		t.Fatal("worker identity did not protect against self-messaging")
	}
}
