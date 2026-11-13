package app

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestGroupControlRecording(t *testing.T) {
	for _, mode := range []string{"accept", "invalid", "error", "cancel", "stale", "owner", "closed", "admission"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			store, err := session.OpenStore(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(t.Context(), session.CreateOptions{ID: "group-recording"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			m := commitTestModel()
			m.services.Recording, m.services.sessionID = owner, owner.ID()
			content := `{"action":"kick","arguments":{"name":"api"}}`
			if mode == "invalid" {
				content = "invalid"
			}
			response := ai.Response{Content: ai.TextContent(content), Usage: ai.Usage{Input: 17, Output: 4}}
			if mode == "error" {
				response.Err = errors.New("provider failed")
			}
			driver := aitest.New(aitest.Replies(response))
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			if mode == "admission" {
				owner.Close()
			}
			_, cmd := m.resolveGroupControl("please remove api")
			if cmd == nil {
				t.Fatal("missing completion")
			}
			msg := cmd().(groupControlMsg)
			switch mode {
			case "cancel":
				m.cancelGroupControl()
			case "stale":
				m.grp.control = nil
			case "owner":
				m.services.Recording = nil
			case "closed":
				owner.Close()
			}
			text := m.handleGroupControl(msg)
			if again := m.handleGroupControl(msg); again != "" && mode != "owner" {
				t.Fatalf("replayed: %q", again)
			}
			p := owner.Snapshot()
			annotations, finishes := 0, 0
			for _, event := range p.Events() {
				switch payload := event.Payload.(type) {
				case *session.AnnotationAddPayload:
					annotations++
					if payload.Text != text || !strings.Contains(text, "explicitly enter: /group kick api") {
						t.Fatalf("not actual response: %+v / %q", payload, text)
					}
				case *session.InferenceStartPayload:
					if payload.Purpose != session.PurposeGroup || payload.Input.Standalone == "" || payload.Input.Conversation != nil {
						t.Fatalf("wrong request: %+v", payload)
					}
				case *session.InferenceFinishPayload:
					finishes++
					if payload.Usage.Input != 17 || payload.Usage.Output != 4 {
						t.Fatalf("usage: %+v", payload)
					}
				}
			}
			want := 0
			if mode == "accept" {
				want = 1
			}
			if annotations != want {
				t.Fatalf("annotations=%d want=%d", annotations, want)
			}
			if mode != "closed" && mode != "admission" && (finishes != 1 || len(p.Unfinished().Inferences) != 0) {
				t.Fatal("scope not closed")
			}
			if mode == "admission" && msg.err == nil {
				t.Fatal("closed admission dispatched")
			}
			if len(p.Messages()) != 0 {
				t.Fatal("group result entered conversation")
			}
		})
	}
}

func TestGroupControlRecordingPostEffectFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "post-effect-group"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	m := commitTestModel()
	m.services.Recording, m.services.sessionID = owner, owner.ID()
	m.services.Reminder = reminder.New()
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent(`{"action":"join","arguments":{"group":"team","name":"api"}}`)})), ai.Model{ID: "m"})}
	_, cmd := m.resolveGroupControl("join team as api")
	msg := cmd().(groupControlMsg)
	// Join asks for the session ID after the handler's pre-effect store check.
	calls := 0
	group.BindSession(func() string { calls++; _ = owner.Close(); return owner.ID() })
	defer group.BindSession(func() string { return "" })
	defer group.Leave()
	text := m.handleGroupControl(msg)
	if !strings.Contains(text, "not rolled back") {
		t.Fatalf("misleading recording failure: %q", text)
	}
	if g, _ := group.Current(); g != "team" {
		t.Fatalf("operation did not complete: %q", g)
	}
	if again := m.handleGroupControl(msg); again != "" || calls != 1 {
		t.Fatalf("operation replay: %q calls=%d", again, calls)
	}
	p := owner.Snapshot()
	for _, event := range p.Events() {
		if event.Type == session.EventAnnotationAdd {
			t.Fatal("uncommitted success published")
		}
	}
}

func TestGroupControlRecordingMixedWriter(t *testing.T) {
	m := commitTestModel()
	m.services.Recording = new(session.Session)
	m.services.Session = new(session.LegacySession)
	if text, cmd := m.resolveGroupControl("show collaborators"); cmd != nil || !strings.Contains(text, "mixed session writers") {
		t.Fatalf("mixed writer accepted: %q", text)
	}
}

// Keep cancellation accounting on the original owner even before dispatch.
func TestGroupControlRecordingCancelledInFlight(t *testing.T) {
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "inflight-group"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	m := commitTestModel()
	m.services.Recording = owner
	entered := make(chan struct{})
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		return aitest.Hangs()(ctx, request)
	})
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
	_, cmd := m.resolveGroupControl("show collaborators")
	done := make(chan groupControlMsg, 1)
	go func() { done <- cmd().(groupControlMsg) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("not dispatched")
	}
	m.cancelGroupControl()
	select {
	case msg := <-done:
		if text := m.handleGroupControl(msg); text != "" {
			t.Fatal(text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not cancelled")
	}
	p := owner.Snapshot()
	events := p.Events()
	if len(events) != 3 || events[2].Type != session.EventInferenceFinish || events[2].Payload.(*session.InferenceFinishPayload).Outcome != session.OutcomeCancelled || len(p.Unfinished().Inferences) != 0 {
		t.Fatalf("cancel not accounted: %+v", events)
	}
}

func TestGroupControlRecordingCancelledBeforeDispatch(t *testing.T) {
	store, err := session.OpenStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "cancel-group"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	m := commitTestModel()
	m.services.Recording = owner
	m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("unused")})), ai.Model{ID: "m"})}
	_, cmd := m.resolveGroupControl("show collaborators")
	m.cancelGroupControl()
	if got := m.handleGroupControl(cmd().(groupControlMsg)); got != "" {
		t.Fatal(got)
	}
	p := owner.Snapshot()
	if len(p.Events()) != 1 {
		t.Fatal("cancelled request dispatched")
	}
}
