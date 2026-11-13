package agent

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func recallFixture(t *testing.T) (*session.Session, system.MemoryStores) {
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
	if err := os.WriteFile(filepath.Join(dir, "freeze.md"), []byte("---\ndescription: release freeze\n---\nno merges"), 0600); err != nil {
		t.Fatal(err)
	}
	return owner, system.MemoryStores{Project: dir}
}

func TestRecallRecording(t *testing.T) {
	for _, mode := range []string{"selected", "parse", "empty", "partial-error"} {
		t.Run(mode, func(t *testing.T) {
			owner, stores := recallFixture(t)
			text := `{"selected_memories":["project/freeze.md"]}`
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
			got, err := (Recaller{Provider: compactRecordingProvider{driver}, Model: "model", Recording: owner}).Recall(t.Context(), stores, "can I merge?", nil)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got) == 1) != (mode == "selected") {
				t.Fatalf("memories=%v", got)
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
		})
	}
}

func TestRecallClosedRecording(t *testing.T) {
	owner, stores := recallFixture(t)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["project/freeze.md"]}`))
	pick := (Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}).Start(stores, "q", nil)
	<-pick.ready
	_, err := WithRecall(nil, func() *Recall { return pick })(t.Context(), sdkagent.PostToolContext{Result: sdkagent.Result{Content: ai.TextContent("ok")}})
	var recordingErr *llm.RecordingError
	if !errors.Is(err, session.ErrClosed) || !errors.As(err, &recordingErr) || driver.Calls() != 0 || len(pick.Attached()) != 0 {
		t.Fatalf("err=%v calls=%d attached=%v", err, driver.Calls(), pick.Attached())
	}
	if err := pick.Stop(); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("Stop lost error: %v", err)
	}
}

func TestRecallFinishRecordingFailure(t *testing.T) {
	owner, stores := recallFixture(t)
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		return aitest.Says(`{"selected_memories":["project/freeze.md"]}`)(ctx, request)
	})
	pick := (Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}).Start(stores, "q", nil)
	<-pick.ready
	text, err := pick.Take()
	var recordingErr *llm.RecordingError
	if text != "" || !errors.Is(err, session.ErrClosed) || !errors.As(err, &recordingErr) || len(pick.Attached()) != 0 {
		t.Fatalf("failed finish delivered selection: text=%q err=%v", text, err)
	}
	if _, again := pick.Take(); !errors.Is(again, session.ErrClosed) {
		t.Fatalf("lost retained error: %v", again)
	}
}

func TestRecallStopRetainsOriginalOwner(t *testing.T) {
	owner, stores := recallFixture(t)
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	replacement, err := store.Create(t.Context(), session.CreateOptions{ID: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		<-release
		return aitest.Replies(ai.Response{Content: ai.TextContent(`{"selected_memories":["project/freeze.md"]}`), Usage: ai.Usage{Input: 17, Output: 4}})(context.WithoutCancel(ctx), request)
	})
	r := Recaller{Provider: compactRecordingProvider{driver}, Recording: owner}
	pick := r.Start(stores, "q", nil)
	r.Recording = replacement
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- pick.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("Stop joined provider")
	}
	close(release)
	<-pick.ready
	if text, err := pick.Take(); text != "" || err != nil {
		t.Fatalf("discarded pick=%q err=%v", text, err)
	}
	originalProjection, replacementProjection := owner.Snapshot(), replacement.Snapshot()
	if len(pick.Attached()) != 0 || len(replacementProjection.Events()) != 1 || len(originalProjection.Unfinished().Inferences) != 0 {
		t.Fatal("late selection attached, retargeted, or lost finish")
	}
	count := 0
	for _, event := range originalProjection.Events() {
		if finish, ok := event.Payload.(*session.InferenceFinishPayload); ok {
			count++
			if finish.Usage.Input != 17 {
				t.Fatal("lost late usage")
			}
		}
	}
	if count != 1 {
		t.Fatalf("finishes=%d", count)
	}
}
