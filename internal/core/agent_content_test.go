package core

import (
	"iter"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/sdk-go/pkg/ai"

	"context"
	"testing"
	"time"
)

// The turn loop reports Content from a single tracked value rather than from
// each return site. These tests pin the two exits that used to supply it by
// hand and got it wrong: the step cap substituted a status string, and every
// cancellation path substituted "". Both discarded work the model had already
// done and the caller had already paid for.

// noopTool lets a scripted response carry a tool call, which is what keeps the
// turn loop iterating instead of ending on the first step.
type noopTool struct{}

func (noopTool) Schema() ToolSchema { return ToolSchema{Name: "noop", Description: "does nothing"} }
func (noopTool) Run(context.Context, ai.ToolCall) (sdkagent.Result, error) {
	return sdkagent.TextResult("done"), nil
}

// talks is a model that says something and asks for a tool, so the turn never
// reaches end_turn on its own and the loop exits through a guard.
func talks(text string) aitest.Turn {
	content := ai.TextContent(text)
	content = append(content, ai.ToolCallBlock(ToolCall{ID: "call-1", Name: "noop", Input: "{}"}))
	return aitest.Replies(ai.Response{Content: content, StopReason: ai.StopToolUse})
}

// newContentAgent mirrors newRetryAgent in retry_test.go: build, seed a user
// message, and drain the outbox, which emit() blocks on once the buffer fills.
func newContentAgent(d ai.Driver, maxSteps int) *agent {
	ag := NewAgent(Config{
		ID:       "test",
		Client:   testClient(d),
		System:   NewSystem(),
		Tools:    NewTools(noopTool{}),
		MaxSteps: maxSteps,
	}).(*agent)
	ag.Append(context.Background(), UserMessage("go", nil))
	go func() {
		for range ag.Outbox() {
		}
	}()
	return ag
}

func TestThinkActReportsModelOutputWhenStepCapHits(t *testing.T) {
	const said = "partial analysis of the repo"
	ag := newContentAgent(aitest.Always(talks(said)), 2)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := ag.ThinkAct(ctx)
	if err != nil {
		t.Fatalf("ThinkAct returned error: %v", err)
	}
	if result.StopReason != StopMaxSteps {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, StopMaxSteps)
	}
	// The cap is already reported through StopReason; Content must not be
	// overwritten with a status string, or a subagent that hits its cap hands
	// its parent that string in place of the work.
	if result.Content != said {
		t.Errorf("Content = %q, want the model's last output %q", result.Content, said)
	}
	if result.Steps != 2 {
		t.Errorf("Steps = %d, want 2", result.Steps)
	}
}

func TestThinkActPreservesPartialOutputOnCancel(t *testing.T) {
	// Bounded as well as cancellable: talkingLLM always answers with a tool
	// call, so if the cancel hook below ever stopped firing the loop would spin
	// until go test's global timeout instead of failing here.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const said = "found the bug in fs_store"
	// Cancel on the second inference, so step one has fully completed and its
	// assistant message is already appended. Cancelling during the first
	// inference would be a different case: no assistant message exists yet, and
	// an empty Content is then the honest answer rather than a lost one.
	calls := 0
	ag := newContentAgent(aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		calls++
		if calls == 2 {
			cancel()
		}
		return talks(said)(ctx, req)
	}), 0)

	result, err := ag.ThinkAct(ctx)
	if err == nil {
		t.Fatal("ThinkAct returned nil error on a cancelled turn")
	}
	if result == nil {
		t.Fatal("ThinkAct returned nil Result on cancel; observers need the turn boundary")
	}
	if result.StopReason != StopCanceled {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, StopCanceled)
	}
	// subagent.buildUnfinishedAgentResult documents that a cancelled run's
	// partial content travels back with the error, and RunBackground gates that
	// on Content != "". An empty string here makes that feature dead code.
	if result.Content != said {
		t.Errorf("Content = %q, want the partial output %q", result.Content, said)
	}
}

// TestThinkActReportsEmptyContentWhenTurnEndsOnToolOnlyStep pins the deliberate
// half of the tracking rule. lastContent follows the newest assistant message
// unconditionally, so a turn whose last step is pure tool calls reports "" —
// it does not fall back to narration from an earlier step. That fallback would
// present stale text as this turn's outcome, so the emptiness is the intended
// answer rather than a gap to close.
func TestThinkActReportsEmptyContentWhenTurnEndsOnToolOnlyStep(t *testing.T) {
	// Quiet after the first step: the turn ends on a tool-only step carrying
	// no assistant text.
	ag := newContentAgent(aitest.New(talks("starting the sweep"), talks("")), 2)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := ag.ThinkAct(ctx)
	if err != nil {
		t.Fatalf("ThinkAct returned error: %v", err)
	}
	if result.StopReason != StopMaxSteps {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, StopMaxSteps)
	}
	if result.Content != "" {
		t.Errorf("Content = %q, want \"\" — the last step carried no assistant text", result.Content)
	}
}
