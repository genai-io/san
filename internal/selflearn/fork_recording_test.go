package selflearn

import (
	"errors"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func reviewRecording(t *testing.T) *session.Session {
	t.Helper()
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "review", AgentID: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner
}

func TestRunReviewNativeUsage(t *testing.T) {
	owner := reviewRecording(t)
	mgr, _ := newTestSkillManager(t)
	llm := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("nothing to save"), Usage: ai.Usage{Input: 17, Output: 4}}))
	summary, err := RunReview(t.Context(), ForkConfig{Client: stubClient(llm), Skills: mgr, Recording: owner}, nil)
	if err != nil || summary != "nothing to save" {
		t.Fatalf("summary=%q err=%v", summary, err)
	}
	snapshot := owner.Snapshot()
	for _, event := range snapshot.Events() {
		if p, ok := event.Payload.(*session.InferenceFinishPayload); ok {
			if p.Usage.Input != 17 || p.Usage.Output != 4 {
				t.Fatalf("usage=%+v", p.Usage)
			}
			return
		}
	}
	t.Fatal("review usage was not recorded")
}

func TestRunReviewClosedOwnerFailsBeforeDispatch(t *testing.T) {
	owner := reviewRecording(t)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	mgr, _ := newTestSkillManager(t)
	llm := aitest.New(aitest.Says("must not dispatch"))
	_, err := RunReview(t.Context(), ForkConfig{Client: stubClient(llm), Skills: mgr, Recording: owner}, nil)
	if !errors.Is(err, session.ErrClosed) || llm.Calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, llm.Calls())
	}
}
