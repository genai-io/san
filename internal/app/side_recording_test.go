package app

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRecapRecordingCancelledOnRetirement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "cancel-recap"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	m := newRecapModel(t)
	m.services.Recording, m.services.sessionID = owner, owner.ID()
	entered := make(chan struct{})
	driver := aitest.New(func(ctx context.Context, r *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		return aitest.Hangs()(ctx, r)
	})
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
	cmd := m.writeRecap()
	done := make(chan recapReadyMsg, 1)
	go func() { done <- cmd().(recapReadyMsg) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("recap not dispatched")
	}
	m.resetRecap()
	var ready recapReadyMsg
	select {
	case ready = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("retired recap did not cancel")
	}
	// A queued old message must not clear the replacement request's spinner.
	m.writeRecap()
	defer m.resetRecap()
	m.recap.show = true
	m.onRecapReady(ready)
	if !m.recap.busy || !m.recap.show {
		t.Fatal("old result changed replacement state")
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 3 || events[2].Type != session.EventInferenceFinish ||
		events[2].Payload.(*session.InferenceFinishPayload).Outcome != session.OutcomeCancelled || len(p.Unfinished().Inferences) != 0 {
		t.Fatalf("cancelled recap was not accounted: %+v", events)
	}
}

func TestRecapRecordingAcceptance(t *testing.T) {
	for _, mode := range []string{"show", "advance", "overwrite", "switch", "closed", "reset"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, err := session.OpenStore(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(t.Context(), session.CreateOptions{ID: "recap"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			m := newRecapModel(t)
			m.services.Recording, m.services.sessionID = owner, owner.ID()
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Replies(ai.Response{
				Content: ai.TextContent("Ready for review."), Usage: ai.Usage{Input: 17, Output: 4},
			})), ai.Model{ID: "m"})}
			ready := m.writeRecap()().(recapReadyMsg)
			if ready.err != nil {
				t.Fatal(ready.err)
			}
			before := len(m.conv.Messages)
			m.onRecapReady(ready)
			if !m.recapReady() || len(m.conv.Messages) != before {
				t.Fatal("prewarm displayed before acceptance")
			}
			p := owner.Snapshot()
			if len(p.Events()) != 2 || len(p.Unfinished().Inferences) != 1 {
				t.Fatal("prewarm published an annotation or lost deferred completion")
			}
			switch mode {
			case "show":
				m.deliverRecap()
				// A repeated delivery of the same cached message must not publish twice.
				m.showRecap(ready)
			case "advance":
				m.services.Agent.SetMessages([]core.Message{core.UserMessage("later", nil)})
				m.deliverRecap() // discards the old cache before preparing another request
				m.resetRecap()
			case "overwrite":
				m.writeRecap()
				m.resetRecap()
			case "switch":
				m.resetRecap()
				m.services.Recording = nil
				m.showRecap(ready)
			case "closed":
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
				m.showRecap(ready)
			case "reset":
				m.resetRecap()
			}
			p = owner.Snapshot()
			annotations, finishes := 0, 0
			for _, event := range p.Events() {
				switch payload := event.Payload.(type) {
				case *session.AnnotationAddPayload:
					annotations++
					if payload.Text != conv.RecapNotice(m.recapFacts(), ready.text) {
						t.Fatalf("wrong delivered recap: %+v", payload)
					}
				case *session.InferenceStartPayload:
					if payload.Purpose != session.PurposeRecap {
						t.Fatalf("purpose = %q", payload.Purpose)
					}
				case *session.InferenceFinishPayload:
					finishes++
					if payload.Usage.Input != 17 || payload.Usage.Output != 4 {
						t.Fatalf("lost usage: %+v", payload)
					}
				}
			}
			want := 0
			if mode == "show" {
				want = 1
			}
			if annotations != want || len(m.conv.Messages) != before+want {
				t.Fatalf("annotations=%d displayed=%d want=%d", annotations, len(m.conv.Messages)-before, want)
			}
			if mode != "closed" && (finishes != 1 || len(p.Unfinished().Inferences) != 0) {
				t.Fatal("discarded cache leaked its completion")
			}
			if len(p.Messages()) != 0 {
				t.Fatal("recap text leaked into main context")
			}
		})
	}
}

func TestSideRecordingBtwOwnership(t *testing.T) {
	for _, mode := range []string{"current", "stale", "closed", "default"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			store, err := session.OpenStore(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(ctx, session.CreateOptions{ID: "side-test"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			var recording *session.Session
			if mode != "default" {
				recording = owner
			}
			messages := withSidePrompt([]core.Message{core.UserMessage("work", nil)}, "why?")
			for i := range messages {
				if messages[i].ID == "" {
					messages[i].ID = core.NewMessageID()
				}
			}
			provider := &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Says("because")), ai.Model{ID: "m"})}
			req := sideRequest{label: "btw", client: llm.NewClient(provider, "m", 100), messages: messages, owner: recording, recording: recording.Completion("btw", messages[1:]...)}
			answer := askSide(ctx, req)
			if answer != "because" {
				t.Fatalf("answer = %q", answer)
			}
			if mode == "closed" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			m := newRecapModel(t)
			m.services.sessionID = "same"
			m.services.Recording = recording
			before := len(m.conv.Messages)
			if mode == "stale" {
				m.services.Recording = nil
			}
			m.onBtwAnswer(btwAnswerMsg{ask: btwAsk{question: "why?", sessionID: "same"}, answer: answer, owner: recording, recording: req.recording})
			accepted := mode == "current" || mode == "default"
			if (len(m.conv.Messages) > before) != accepted {
				t.Fatalf("accepted output = %v, want %v", m.conv.Messages, accepted)
			}
			snapshot := owner.Snapshot()
			annotations, starts, finishes := 0, 0, 0
			for _, event := range snapshot.Events() {
				switch event.Type {
				case session.EventAnnotationAdd:
					annotations++
				case session.EventInferenceStart:
					starts++
					if mode == "closed" {
						continue
					}
					native, err := owner.InferenceRequest(ctx, event.Seq)
					if err != nil {
						t.Fatal(err)
					}
					if len(native.Messages) != len(messages) {
						t.Fatalf("native request lost messages: %+v", native)
					}
				case session.EventInferenceFinish:
					finishes++
				}
			}
			if mode == "default" {
				if starts != 0 {
					t.Fatal("default path recorded")
				}
				return
			}
			if starts != 1 {
				t.Fatalf("starts = %d", starts)
			}
			if mode != "closed" && finishes != 1 {
				t.Fatalf("finishes = %d", finishes)
			}
			wantAnnotations := 0
			if mode == "current" {
				wantAnnotations = 1
			}
			if annotations != wantAnnotations {
				t.Fatalf("annotations = %d", annotations)
			}
			if len(snapshot.Messages()) != 0 {
				t.Fatal("side prompt or answer entered main context")
			}
		})
	}
}
