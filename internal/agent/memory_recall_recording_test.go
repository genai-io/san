package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func recallFixture(t *testing.T) (*session.Session, system.MemoryStore) {
	t.Helper()
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "recall"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close() })
	dir := t.TempDir()
	writeRecallNote(t, dir, "freeze.md", "---\ndescription: release freeze\n---\nno merges")
	return owner, system.MemoryStore(dir)
}

// writeRecallNote writes a note and links it from MEMORY.md, as Recall lists
// only linked notes.
func writeRecallNote(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(dir, system.AutoMemoryIndexName)
	index, err := os.ReadFile(indexPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if link := "(" + name + ")"; !strings.Contains(string(index), link) {
		index = fmt.Appendf(index, "- [%s]%s\n", name, link)
		if err := os.WriteFile(indexPath, index, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestRecallRecording(t *testing.T) {
	for _, mode := range []string{"selected", "parse", "empty", "partial-error"} {
		t.Run(mode, func(t *testing.T) {
			owner, store := recallFixture(t)
			text := `{"selected_memories":["freeze.md"]}`
			if mode == "parse" {
				text = "not JSON"
			}
			if mode == "empty" {
				text = ""
			}
			var callErr error
			if mode == "partial-error" {
				callErr = errors.New("provider partial failure")
			}
			driver := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}, Err: callErr}))
			msg := core.UserMessage("can I merge?", nil)
			got, err := (Recaller{Provider: compactRecordingProvider{driver}, Model: "model", Recording: owner}).Prepare(t.Context(), store, msg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (got.Completion != nil) != (mode == "selected") || strings.Contains(got.Message.Text(), "no merges") != (mode == "selected") {
				t.Fatalf("prepared input=%+v", got)
			}
			if err := got.Completion.Close(); err != nil {
				t.Fatal(err)
			}
			p := owner.Snapshot()
			if len(p.Messages()) != 0 || len(p.ActiveMessages()) != 0 || len(p.Unfinished().Inferences) != 0 {
				t.Fatal("selection changed conversation or left unfinished inference")
			}
			var start *session.InferenceStartPayload
			var finish *session.InferenceFinishPayload
			for _, event := range p.Events() {
				switch payload := event.Payload.(type) {
				case *session.InferenceStartPayload:
					start = payload
				case *session.InferenceFinishPayload:
					finish = payload
				default:
					if event.Type != session.EventSessionCreate {
						t.Fatalf("unexpected selection event %s", event.Type)
					}
				}
			}
			if start == nil || start.Purpose != session.PurposeRecall || start.Input.Standalone == "" || start.Input.Conversation != nil {
				t.Fatalf("start=%+v", start)
			}
			request, err := owner.Request(t.Context(), start.Input.Standalone)
			if err != nil {
				t.Fatal(err)
			}
			effective := driver.Last()
			if request.System != recallSystemPrompt || request.System != effective.System || !reflect.DeepEqual(request.Messages, effective.Messages) || !reflect.DeepEqual(request.Tools, effective.Tools) {
				t.Fatalf("recorded request differs: %+v vs %+v", request, effective)
			}
			if len(request.Messages) != 3 {
				t.Fatalf("unexpected selector messages: %+v", request.Messages)
			}
			for _, message := range request.Messages {
				if message.Role != ai.RoleUser || len(message.Content) != 1 {
					t.Fatalf("expected one text block per user message: %+v", message)
				}
			}
			if request.Messages[0].Text() != "- [freeze.md](freeze.md)\n" ||
				request.Messages[1].Text() != "Already in main context:\nNone." || request.Messages[2].Text() != "can I merge?" {
				t.Fatalf("selector index, active files, and question were not separated: %+v", request.Messages)
			}
			if finish == nil || finish.Usage.Input != 17 || finish.Usage.Output != 4 {
				t.Fatalf("finish=%+v", finish)
			}
			want := session.OutcomeSuccess
			if mode == "partial-error" {
				want = session.OutcomeFailure
			}
			if finish.Outcome != want {
				t.Fatalf("outcome=%s want=%s", finish.Outcome, want)
			}
			if callErr != nil {
				if finish.Output != nil {
					t.Fatalf("failed attempt recorded partial output: %+v", finish.Output)
				}
			} else if finish.Output == nil || finish.Output.Text != text || finish.Output.Truncated {
				t.Fatalf("output=%+v, want %q", finish.Output, text)
			}
		})
	}
}

func TestRecallClosedRecording(t *testing.T) {
	owner, store := recallFixture(t)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["freeze.md"]}`))
	msg := core.UserMessage("q", nil)
	got, err := (Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}).Prepare(t.Context(), store, msg, nil)
	var recordingErr *llm.RecordingError
	if !errors.Is(err, session.ErrClosed) || !errors.As(err, &recordingErr) || driver.Calls() != 0 || got.Completion != nil || got.Message.Text() != msg.Text() {
		t.Fatalf("err=%v calls=%d prepared=%+v", err, driver.Calls(), got)
	}
}

func TestRecallDiscardRecordingFailure(t *testing.T) {
	owner, store := recallFixture(t)
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		return aitest.Says(`{"selected_memories":[]}`)(ctx, request)
	})
	msg := core.UserMessage("q", nil)
	got, err := (Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}).Prepare(t.Context(), store, msg, nil)
	var recordingErr *llm.RecordingError
	if got.Message.Text() != msg.Text() || got.Completion != nil || !errors.Is(err, session.ErrClosed) || !errors.As(err, &recordingErr) {
		t.Fatalf("failed finish delivered selection: prepared=%+v err=%v", got, err)
	}
}

func TestRecallCancellationRetainsOriginalOwner(t *testing.T) {
	owner, store := recallFixture(t)
	replacement, _ := recallFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := Recaller{Recording: owner}
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		r.Recording = replacement
		cancel()
		return aitest.Says(`{"selected_memories":["freeze.md"]}`)(context.WithoutCancel(ctx), request)
	})
	r.Provider = compactRecordingProvider{driver}
	msg := core.UserMessage("q", nil)
	got, err := r.Prepare(ctx, store, msg, nil)
	if !errors.Is(err, context.Canceled) || got.Completion != nil || got.Message.Text() != msg.Text() {
		t.Fatalf("canceled selection delivered input: prepared=%+v err=%v", got, err)
	}
	originalProjection, replacementProjection := owner.Snapshot(), replacement.Snapshot()
	if len(originalProjection.Events()) != 3 || len(originalProjection.Unfinished().Inferences) != 0 || len(replacementProjection.Events()) != 1 {
		t.Fatal("canceled selection lost its finish or was retargeted")
	}
}
