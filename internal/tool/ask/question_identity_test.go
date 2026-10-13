package ask

import (
	"context"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestConcurrentQuestionsHaveIndependentIDs(t *testing.T) {
	ask := NewAskUserQuestionTool()
	ids := make(chan string, 100)
	var workers sync.WaitGroup
	for range cap(ids) {
		workers.Go(func() {
			req, err := ask.PrepareInteraction(context.Background(), map[string]any{"question": "Pick one", "options": []string{"one", "two"}}, "/repo")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- req.(*tool.QuestionRequest).ID
		})
	}
	workers.Wait()
	close(ids)
	seen := make(map[string]bool)
	for id := range ids {
		if id == "" || seen[id] {
			t.Fatalf("question identity reused: %q", id)
		}
		seen[id] = true
	}
	if len(seen) != cap(ids) {
		t.Fatal("some questions did not prepare")
	}
}
