package app

import (
	"context"
	"iter"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func recordedCompactModel(t *testing.T, turn aitest.Turn) (*model, *session.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "manual-compact"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	m := newRecapModel(t)
	m.services.Reminder = reminder.New()
	m.env.CWD = t.TempDir()
	m.services.Recording, m.services.sessionID = owner, owner.ID()
	messages := []core.Message{core.UserMessage("first", nil), core.UserMessage("second", nil), core.UserMessage("third", nil)}
	for i := range messages {
		messages[i].ID = core.NewMessageID()
	}
	m.services.Agent.SetMessages(messages)
	if err := owner.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: messages}}); err != nil {
		t.Fatal(err)
	}
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(turn), ai.Model{ID: "m"})}
	t.Cleanup(m.cancelManualCompaction)
	return m, owner
}

func TestManualCompactionRecordingAcceptance(t *testing.T) {
	for _, mode := range []string{"accepted", "switched", "superseded", "advanced", "cancelled", "closed", "empty"} {
		t.Run(mode, func(t *testing.T) {
			text := "condensed"
			if mode == "empty" {
				text = " "
			}
			m, owner := recordedCompactModel(t, aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}}))
			before := m.services.Agent.Messages()
			req := m.BuildCompactRequest("keep the goal", "manual")
			msg := conv.CompactCmd(req)().(conv.CompactResultMsg)
			p := owner.Snapshot()
			if len(p.Unfinished().Inferences) != 1 && mode != "empty" {
				t.Fatal("result accepted before UI ownership check")
			}
			switch mode {
			case "switched":
				m.services.Recording = nil
			case "superseded":
				m.BuildCompactRequest("new focus", "manual")
			case "advanced":
				next := core.UserMessage("later", nil)
				next.ID = core.NewMessageID()
				before = append(before, next)
				m.services.Agent.SetMessages(before)
				if err := owner.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: []core.Message{next}}}); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				m.cancelManualCompaction()
			case "closed":
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			pending := m.pendingCompaction
			m.HandleCompactResult(msg)
			if mode == "superseded" && m.pendingCompaction != pending {
				t.Fatal("old result changed replacement request")
			}
			p = owner.Snapshot()
			compacts, finishes := 0, 0
			for _, event := range p.Events() {
				switch payload := event.Payload.(type) {
				case *session.ContextCompactPayload:
					compacts++
					if payload.Summary.Text() != core.FormatCompactSummary(msg.Summary) || payload.Summary.ID != m.services.Agent.Messages()[0].ID {
						t.Fatal("recorded a different native summary")
					}
				case *session.InferenceFinishPayload:
					finishes++
					if payload.Usage.Input != 17 || payload.Usage.Output != 4 {
						t.Fatalf("lost usage: %+v", payload)
					}
				}
			}
			want := 0
			if mode == "accepted" {
				want = 1
			}
			if compacts != want {
				t.Fatalf("context boundaries=%d want=%d", compacts, want)
			}
			if mode != "closed" && finishes != 1 {
				t.Fatalf("finishes=%d", finishes)
			}
			if mode != "accepted" && !reflect.DeepEqual(before, m.services.Agent.Messages()) {
				t.Fatal("discarded/failed compaction replaced native history")
			}
			if mode == "closed" && !m.conv.Compact.LastError {
				t.Fatal("storage failure was not surfaced")
			}
		})
	}
}

func TestManualCompactionRejectsMixedRecording(t *testing.T) {
	m, owner := recordedCompactModel(t, func(context.Context, *ai.Request) iter.Seq2[ai.Delta, error] {
		t.Fatal("mixed recording dispatched a model request")
		return nil
	})
	legacyStore, err := session.NewLegacyStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyStore.Create(session.LegacyCreateOptions{ID: owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	m.services.Session = legacy
	req := m.BuildCompactRequest("", "manual")
	result := req.Run()
	if result.Err == nil {
		t.Fatal("mixed recording accepted")
	}
	p := owner.Snapshot()
	if len(p.Events()) != 2 {
		t.Fatal("rejected request wrote typed inference events")
	}
}

func TestManualCompactionRecordingCancellation(t *testing.T) {
	entered := make(chan struct{})
	m, owner := recordedCompactModel(t, func(ctx context.Context, r *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		return aitest.Hangs()(ctx, r)
	})
	req := m.BuildCompactRequest("", "manual")
	done := make(chan conv.CompactResultMsg, 1)
	go func() { done <- conv.CompactCmd(req)().(conv.CompactResultMsg) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("model did not start")
	}
	m.cancelManualCompaction()
	select {
	case msg := <-done:
		m.HandleCompactResult(msg)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle")
	}
	p := owner.Snapshot()
	var cancelled bool
	for _, event := range p.Events() {
		if event.Type == session.EventContextCompact {
			t.Fatal("cancelled summary was published")
		}
		if finish, ok := event.Payload.(*session.InferenceFinishPayload); ok {
			cancelled = finish.Outcome == session.OutcomeCancelled
		}
	}
	if !cancelled || len(p.Unfinished().Inferences) != 0 {
		t.Fatal("lost cancelled attempt")
	}
}
