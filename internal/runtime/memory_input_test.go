package runtime

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRecallUsesTurnContextWithoutAddingDeadline(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if _, ok := ctx.Deadline(); ok {
			t.Error("recall added a deadline to the turn context")
		}
		return aitest.Says(`{"selected_memories":["freeze.md"]}`)(ctx, req)
	})
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}
	msg := core.UserMessage("Can I merge?", nil)
	got, err := r.PrepareForAdmission(t.Context(), store, msg, nil)
	if err != nil || got.Completion == nil || !strings.Contains(got.Message.Text(), "no merges") {
		t.Fatalf("recall did not enrich the original input: %+v, %v", got, err)
	}
	if err := got.Completion.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareMemoryRecordsOnlyAcceptedInput(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.New(aitest.Says(`{"selected_memories":["freeze.md"]}`))
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}
	userInput := core.UserMessage("/merge", nil)
	msg := core.UserMessage("Expanded merge instructions", nil)
	msg.ID = "memory-input"
	got, err := r.PrepareWithSubmission(t.Context(), store, userInput, msg, nil)
	if err != nil || !got.Completion.OwnedBy(owner) {
		t.Fatalf("prepare did not return the original owner's completion: %v", err)
	}
	if p := owner.Snapshot(); len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 1 {
		t.Fatal("recall committed before input acceptance")
	}
	request, err := owner.InferenceRequest(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 3 {
		t.Fatalf("expected index, context, and query messages: %+v", request.Messages)
	}
	if text := request.Messages[2].Text(); text != "User input:\n"+userInput.Text() {
		t.Fatalf("expected the labeled original input, not the expanded command: %q", text)
	}
	if !strings.HasPrefix(got.Message.Text(), msg.Text()) {
		t.Fatalf("expanded command missing from prepared input: %q", got.Message.Text())
	}
	if err := got.Completion.AcceptMessage(t.Context(), got.Message); err != nil {
		t.Fatal(err)
	}
	p := owner.Snapshot()
	if len(p.Unfinished().Inferences) != 0 {
		t.Fatal("accepted recall left an unfinished inference")
	}
	events := p.Events()
	if len(events) != 4 || events[3].Type != session.EventInferenceFinish {
		t.Fatalf("unexpected accepted recall events: %+v", events)
	}
	appendInput, ok := events[2].Payload.(*session.MessageAppendPayload)
	if !ok || !appendInput.Accepted || len(appendInput.Messages) != 1 || appendInput.Messages[0].ID != msg.ID || appendInput.Messages[0].Text() != got.Message.Text() || !strings.Contains(got.Message.Text(), "no merges") {
		t.Fatalf("did not record the enriched user input: %+v", events[3])
	}
}

func TestPrepareMemoryDedupesUnchangedNotesInHistory(t *testing.T) {
	owner, store := recallFixture(t)
	const selection = `{"selected_memories":["freeze.md"]}`
	driver := aitest.New(aitest.Says(selection), aitest.Says(selection), aitest.Says(selection))
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}
	msg := core.UserMessage("can I merge?", nil)
	prepare := func(history []core.Message) PreparedMemory {
		t.Helper()
		got, err := r.Prepare(t.Context(), store, msg, history)
		if err != nil {
			t.Fatal(err)
		}
		if err := got.Completion.Close(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := prepare(nil)
	blocks := reminder.Blocks(first.Message.Text())
	if first.Completion == nil || first.Message.Role != msg.Role || !strings.HasPrefix(first.Message.Text(), msg.Text()) || len(blocks) != 1 || blocks[0].Source != reminder.ProviderMemoryRecall || !strings.Contains(blocks[0].Text, "no merges") {
		t.Fatalf("missing recall in prepared user input: %q", first.Message.Text())
	}
	history := []core.Message{first.Message}
	if got := prepare(history); got.Completion != nil || got.Message.Text() != msg.Text() {
		t.Fatalf("repeated unchanged memory: %q", got.Message.Text())
	}
	if err := os.WriteFile(filepath.Join(string(store), "freeze.md"), []byte("merges are open again"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := prepare(history); got.Completion == nil || !strings.Contains(got.Message.Text(), "merges are open again") {
		t.Fatalf("updated note not included: %q", got.Message.Text())
	}
	if p := owner.Snapshot(); len(p.Messages()) != 0 || len(p.Unfinished().Inferences) != 0 {
		t.Fatal("preparation changed conversation or left unfinished inference")
	}
}
