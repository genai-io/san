package conv

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestActivityBurstKeepsNewestAndWorkerMetadata(t *testing.T) {
	bridge := NewAgentToUI(8)
	bridge.SendForToolCall("quiet", "Read(quiet.go)")
	bridge.SendForToolCall("noisy", "Model: worker-model")
	bridge.SendForToolCall("noisy", "Mode: Explore")
	for i := range 101 {
		bridge.SendForToolCall("noisy", fmt.Sprintf("Read(event-%d.go)", i))
	}
	bridge.SendForToolCall("noisy", "Usage: input=200 output=10")
	batch := bridge.takeActivity()
	m := NewModel(80)
	m.Tool.Track([]core.ToolCall{{ID: "quiet", Name: "Agent"}, {ID: "noisy", Name: "Agent"}})
	for _, msg := range batch {
		m.applyActivity(msg)
	}
	quiet, noisy := strings.Join(m.TaskActivity[0], "\n"), strings.Join(m.TaskActivity[1], "\n")
	for _, want := range []string{"worker-model", "Mode: Explore", "Usage: input=200 output=10", "event-100.go", "Omitted: 93"} {
		if !strings.Contains(noisy, want) {
			t.Errorf("burst lost %q: %s", want, noisy)
		}
	}
	if !strings.Contains(quiet, "quiet.go") || len(agentToolLines(m.TaskActivity[1], 0)) != 8 {
		t.Fatalf("sibling lost or feed unbounded: quiet=%s noisy=%s", quiet, noisy)
	}
	if len(bridge.takeActivity()) != 0 {
		t.Fatal("drain left pending activity")
	}
	bridge.SendForToolCall("noisy", "Read(final.go)")
	for _, msg := range bridge.takeActivity() {
		m.applyActivity(msg)
	}
	if agentModel(m.TaskActivity[1], "") != "worker-model" {
		t.Fatal("next drain erased metadata")
	}
	m.Tool.ClearPending()
	m.Tool.Track([]core.ToolCall{{ID: "successor", Name: "Agent"}})
	m.TaskActivity = nil
	m.applyActivity(AgentActivityMsg{Index: 0, ToolCallID: "noisy", Message: "stale"})
	if len(m.TaskActivity) != 0 {
		t.Fatal("old worker activity landed on successor")
	}
}

func TestActivityPollPrioritizesQuestionAndDrainsBurst(t *testing.T) {
	bridge := NewAgentToUI(100)
	for i := range 50 {
		bridge.SendForToolCall("worker", fmt.Sprint(i))
	}
	bridge.qch <- QuestionRequestMsg{Index: 7}
	if question, ok := bridge.Check()().(QuestionRequestMsg); !ok || question.Index != 7 {
		t.Fatal("activity delayed an interactive question")
	}
	batch, ok := bridge.Check()().(AgentActivityBatchMsg)
	if !ok || len(batch) != 50 || batch[len(batch)-1].Message != "49" {
		t.Fatalf("poll did not drain newest progress in one batch: %#v", batch)
	}
}

func TestActivityBridgeConcurrentWorkers(t *testing.T) {
	bridge := NewAgentToUI(12)
	var workers sync.WaitGroup
	for i := range 4 {
		workers.Go(func() {
			for n := range 100 {
				bridge.SendForToolCall(fmt.Sprint(i), fmt.Sprintf("event-%d", n))
			}
		})
	}
	workers.Wait()
	last := make(map[string]string)
	for _, msg := range bridge.takeActivity() {
		last[msg.ToolCallID] = msg.Message
	}
	for i := range 4 {
		if last[fmt.Sprint(i)] != "event-99" {
			t.Fatalf("worker %d lost final progress: %v", i, last)
		}
	}
}
