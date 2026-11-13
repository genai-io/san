package session

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// This provider supplies a real SDK client: preparation, admission, streaming,
// and retries remain on the native llm path, without remote credentials.
type compactionRecordingProvider struct{ client *ai.Client }

func (p compactionRecordingProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}
func (compactionRecordingProvider) Name() string { return "compaction-test" }
func (compactionRecordingProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func TestCompactionRecordingNativeAcceptance(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	r := NewRecorder(s, "test")
	summaryDriver := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("exact summary"), Usage: ai.Usage{Input: 17, Output: 4}}))
	provider := compactionRecordingProvider{ai.NewClientWithDriver(summaryDriver, ai.Model{ID: "resolved-summary"}, ai.WithTemperature(0.4), ai.WithMaxTokens(999))}
	summarizer := llm.NewClient(provider, "alias", 123)
	mainDriver := aitest.New(aitest.Says("done"))
	var admitted *ai.Request
	var startSeq uint64
	a := recorderAgent(r, mainDriver, func(c *core.Config) {
		c.ContextBudget = func() int { return 1 }
		c.CompactFunc = func(ctx context.Context, history []core.Message) (core.Compaction, error) {
			completion := s.Completion(PurposeCompaction)
			admission := completion.Admission()
			response, err := summarizer.Complete(ctx, "summarize independently", []core.Message{core.UserMessage(core.BuildCompactionText(history), nil)}, nil, 0,
				func(ctx context.Context, name string, model ai.Model, request *ai.Request) (llm.CompletionFinish, error) {
					admitted = request
					if name != provider.Name() || model.ID != "resolved-summary" || request.MaxTokens != 123 || request.Temperature == nil || *request.Temperature != 0.4 {
						t.Fatalf("unprepared request: %s %+v %+v", name, model, request)
					}
					finish, err := admission(ctx, name, model, request)
					if err != nil {
						return nil, err
					}
					p := s.Snapshot()
					events := p.Events()
					startSeq = events[len(events)-1].Seq
					start := events[len(events)-1].Payload.(*InferenceStartPayload)
					if start.Purpose != PurposeCompaction || start.Input.Standalone == "" {
						t.Fatalf("not standalone compaction: %+v", start)
					}
					return finish, nil
				})
			return completion.Compaction(response.Content.Text()), err
		}
	})
	old := []core.Message{core.UserMessage("one", nil), core.UserMessage("two", nil), core.UserMessage("three", nil)}
	for i := range old {
		old[i].ID = core.NewMessageID()
		a.Append(old[i])
	}
	if _, err := a.ThinkAct(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if !reflect.DeepEqual(admitted, summaryDriver.Last()) {
		t.Fatal("dispatch differed from admitted SDK request")
	}
	rebuilt, err := s.InferenceRequest(t.Context(), startSeq)
	if err != nil || !sameJSON(rebuilt, admitted) {
		t.Fatalf("standalone request roundtrip: %v", err)
	}
	p := s.Snapshot()
	var compact *ContextCompactPayload
	var boundary, finish Event
	for _, e := range p.Events() {
		if v, ok := e.Payload.(*ContextCompactPayload); ok {
			compact, boundary = v, e
		}
		if e.Type == EventInferenceFinish && e.Source != nil && e.Source.Seq == startSeq {
			finish = e
		}
	}
	if compact == nil {
		t.Fatal("no accepted compaction")
	}
	native := a.Messages()[0]
	if compact.Summary.ID == "" || compact.Summary.ID != native.ID || !sameJSON(compact.Summary, native) || native.Text() != core.FormatCompactSummary("exact summary") {
		t.Fatalf("native summary mismatch: %+v / %+v", compact.Summary, native)
	}
	if boundary.Source == nil || finish.Source == nil || *boundary.Source != *finish.Source || boundary.Source.Seq != startSeq {
		t.Fatalf("result/finish source mismatch: %+v %+v", boundary, finish)
	}
	outcome := finish.Payload.(*InferenceFinishPayload)
	if outcome.Usage.Input != 17 || outcome.Usage.Output != 4 || outcome.Outcome != OutcomeSuccess {
		t.Fatalf("accounting: %+v", outcome)
	}
	foundBatch := false
	for _, batch := range recorderBatches(t, store, s) {
		if reflect.DeepEqual(batch, []string{"context.compact", "inference.finish"}) {
			foundBatch = true
		}
	}
	if !foundBatch {
		t.Fatal("compaction and finish were not one raw batch")
	}
	for _, original := range old {
		found := false
		for _, m := range p.Messages() {
			if m.ID == original.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("lost historical message %s", original.ID)
		}
	}
	active := p.ActiveMessages()
	if len(active) != 2 || active[0].ID != native.ID || active[1].Text() != "done" {
		t.Fatalf("active captured prefix: %+v", active)
	}
	for _, e := range p.Events() {
		if append, ok := e.Payload.(*MessageAppendPayload); ok {
			for _, m := range append.Messages {
				if m.ID == native.ID {
					t.Fatal("observer duplicated committed summary as message.append")
				}
			}
		}
	}
	// Snapshot equality, rather than the event's origin, also fences replacement confirmations.
	a.SetMessages(active)
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	if got := s.Snapshot(); len(got.Events()) != len(p.Events()) {
		t.Fatal("equal native replacement generated events")
	}
}

func TestCompactionRecordingNativeQueuedInput(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	r := NewRecorder(s, "test")
	var a core.Agent
	later := core.UserMessage("queued during compaction", nil)
	later.ID = core.NewMessageID()
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		a.Append(later)
		return aitest.Says("summary")(ctx, request)
	})
	summarizer := llm.NewClient(compactionRecordingProvider{ai.NewClientWithDriver(driver, ai.Model{ID: "summary"})}, "summary", 100)
	a = recorderAgent(r, aitest.Always(aitest.Says("done")), func(c *core.Config) {
		c.ContextBudget = func() int { return 1 }
		c.CompactFunc = func(ctx context.Context, _ []core.Message) (core.Compaction, error) {
			completion := s.Completion(PurposeCompaction)
			response, err := summarizer.Complete(ctx, "summarize", nil, nil, 0, completion.Admission())
			return completion.Compaction(response.Content.Text()), err
		}
	})
	for _, text := range []string{"one", "two", "three"} {
		a.Append(core.UserMessage(text, nil))
	}
	if _, err := a.ThinkAct(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.Err() != nil {
		t.Fatal(r.Err())
	}
	p := s.Snapshot()
	native := a.Messages()
	if !sameJSON(p.ActiveMessages(), native) {
		t.Fatalf("native/projection mismatch: native=%+v active=%+v", native, p.ActiveMessages())
	}
	if len(native) < 3 || native[0].Text() != core.FormatCompactSummary("summary") {
		t.Fatalf("missing summary or queued input: %+v", native)
	}
	found := false
	for _, message := range native[1:] {
		if message.ID == later.ID && message.Text() == later.Text() {
			found = true
		}
	}
	if !found {
		t.Fatalf("native queued input was lost: %+v", native)
	}
}

func TestCompactionRecordingNativeDiscard(t *testing.T) {
	for _, mode := range []string{"empty", "obsolete", "canceled", "partial failure", "retry"} {
		t.Run(mode, func(t *testing.T) {
			s := createTyped(t, cleanStore(t), CreateOptions{})
			r := NewRecorder(s, "test")
			boom := errors.New("partial stream failed")
			response := ai.Response{Content: ai.TextContent("unused"), Usage: ai.Usage{Input: 7, Output: 3}}
			if mode == "empty" {
				response.Content = ai.TextContent("  ")
			}
			if mode == "partial failure" {
				response.Err = boom
			}
			driver := aitest.New(aitest.Replies(response))
			if mode == "retry" {
				driver = aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("partial"), Usage: ai.Usage{Input: 5, Output: 2}, Err: &ai.Error{Kind: ai.KindOverloaded, Message: "retry"}}), aitest.Replies(response))
			}
			summarizer := llm.NewClient(compactionRecordingProvider{ai.NewClientWithDriver(driver, ai.Model{ID: "summary"})}, "summary", 100)
			main := aitest.New(aitest.Says("done"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			a := recorderAgent(r, main, func(c *core.Config) {
				c.ContextBudget = func() int { return 1 }
				c.CompactFunc = func(ctx context.Context, _ []core.Message) (core.Compaction, error) {
					completion := s.Completion(PurposeCompaction)
					resp, err := summarizer.Complete(ctx, "summary", nil, nil, 0, completion.Admission())
					if mode == "canceled" {
						cancel()
					}
					if mode == "obsolete" || mode == "retry" {
						// The owner discards an otherwise successful result before native acceptance.
						if closeErr := completion.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
						return core.Compaction{}, err
					}
					return completion.Compaction(resp.Content.Text()), err
				}
			})
			old := []core.Message{core.UserMessage("one", nil), core.UserMessage("two", nil), core.UserMessage("three", nil)}
			for i := range old {
				old[i].ID = core.NewMessageID()
				a.Append(old[i])
			}
			_, err := a.ThinkAct(ctx)
			if mode == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if r.Err() != nil {
				t.Fatal(r.Err())
			}
			p := s.Snapshot()
			if len(p.Unfinished().Inferences) != 0 {
				t.Fatal("unaccounted discarded attempt")
			}
			starts := map[uint64]bool{}
			var outcomes []*InferenceFinishPayload
			for _, e := range p.Events() {
				if e.Type == EventContextCompact {
					t.Fatal("discard published compaction")
				}
				if v, ok := e.Payload.(*InferenceStartPayload); ok && v.Purpose == PurposeCompaction {
					starts[e.Seq] = true
				}
				if v, ok := e.Payload.(*InferenceFinishPayload); ok && e.Source != nil && starts[e.Source.Seq] {
					outcomes = append(outcomes, v)
				}
			}
			want := 1
			if mode == "retry" {
				want = 2
			}
			if len(outcomes) != want || driver.Calls() != want {
				t.Fatalf("attempts: outcomes=%d calls=%d", len(outcomes), driver.Calls())
			}
			last := outcomes[len(outcomes)-1]
			if last.Usage.Input != 7 || last.Usage.Output != 3 {
				t.Fatalf("lost partial/discard usage: %+v", last)
			}
			if mode == "partial failure" && last.Outcome != OutcomeFailure {
				t.Fatalf("partial outcome: %+v", last)
			}
			if mode == "retry" && (outcomes[0].Outcome != OutcomeFailure || outcomes[0].Usage.Input != 5 || outcomes[0].Usage.Output != 2) {
				t.Fatalf("retry accounting: %+v", outcomes[0])
			}
			native := a.Messages()
			for i := range old {
				if native[i].ID != old[i].ID {
					t.Fatal("discard replaced native history")
				}
			}
		})
	}
}

func TestCompactionRecordingStorageFences(t *testing.T) {
	for _, stage := range []string{"admission", "finish"} {
		t.Run(stage, func(t *testing.T) {
			s := createTyped(t, cleanStore(t), CreateOptions{})
			r := NewRecorder(s, "test")
			driver := aitest.New(aitest.Says("summary"))
			summarizer := llm.NewClient(compactionRecordingProvider{ai.NewClientWithDriver(driver, ai.Model{ID: "summary"})}, "summary", 100)
			main := aitest.New(aitest.Says("must not dispatch"))
			a := recorderAgent(r, main, func(c *core.Config) {
				c.ContextBudget = func() int { return 1 }
				c.CompactFunc = func(ctx context.Context, _ []core.Message) (core.Compaction, error) {
					completion := s.Completion(PurposeCompaction)
					if stage == "admission" {
						if err := s.Close(); err != nil {
							t.Fatal(err)
						}
					}
					response, err := summarizer.Complete(ctx, "summary", nil, nil, 0, completion.Admission())
					if stage == "finish" {
						if closeErr := s.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
					}
					return completion.Compaction(response.Content.Text()), err
				}
			})
			old := []core.Message{core.UserMessage("one", nil), core.UserMessage("two", nil), core.UserMessage("three", nil)}
			for i := range old {
				old[i].ID = core.NewMessageID()
				a.Append(old[i])
			}
			if _, err := a.ThinkAct(t.Context()); err == nil {
				t.Fatal("storage fence did not fail turn")
			}
			wantCalls := 0
			if stage == "finish" {
				wantCalls = 1
			}
			if driver.Calls() != wantCalls || main.Calls() != 0 || !sameJSON(a.Messages(), old) {
				t.Fatalf("crossed fence: summary=%d main=%d history=%+v", driver.Calls(), main.Calls(), a.Messages())
			}
			p := s.Snapshot()
			for _, e := range p.Events() {
				if e.Type == EventContextCompact {
					t.Fatal("failed finish committed compaction")
				}
			}
		})
	}
}
