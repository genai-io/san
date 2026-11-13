package llm

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type preparedCompletionProvider struct {
	mockLLMProvider
	client *ai.Client
}

func (p *preparedCompletionProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}

func TestCompletionRecordingPreparedRequestAndPartialUsage(t *testing.T) {
	boom := errors.New("stream failed")
	driver := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("partial"), Usage: ai.Usage{Input: 17, Output: 4}, Err: boom}))
	p := &preparedCompletionProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved-model"},
		ai.WithMaxTokens(999), ai.WithTemperature(0.4), ai.WithToolChoice(ai.ToolChoiceNone),
	)}
	messages := []ai.Message{{Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "orphan", Name: "Bash", Input: `{}`})}}}
	var admitted *ai.Request
	var finished *ai.Response
	resp, err := Complete(t.Context(), p, CompletionOptions{Model: "alias", MaxTokens: 123, Messages: messages,
		Admission: func(_ context.Context, name string, model ai.Model, r *ai.Request) (CompletionFinish, error) {
			if name != "mock" || model.ID != "resolved-model" || r.MaxTokens != 123 || r.Temperature == nil || *r.Temperature != 0.4 {
				t.Fatalf("not the effective request: provider=%s model=%+v request=%+v", name, model, r)
			}
			admitted = r
			return func(_ context.Context, r *ai.Response, callErr error) error {
				finished = r
				if !errors.Is(callErr, boom) {
					t.Errorf("lost provider error: %v", callErr)
				}
				return nil
			}, nil
		},
	})
	if !errors.Is(err, boom) || finished == nil || finished.Usage.Input != 17 || resp.Usage.Output != 4 || resp.Content.Text() != "partial" {
		t.Fatalf("lost partial response: response=%+v finish=%+v err=%v", resp, finished, err)
	}
	if !reflect.DeepEqual(admitted, driver.Last()) || !reflect.DeepEqual(admitted.Messages, ai.Repair(messages)) {
		t.Fatal("admission did not observe SDK-prepared/repaired request")
	}
}

func TestCompletionRecordingAdmissionFailures(t *testing.T) {
	for _, nilFinish := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "nil finish"}[nilFinish], func(t *testing.T) {
			p := &mockLLMProvider{driver: aitest.New(aitest.Says("unused"))}
			c := NewClient(p, "model", 100)
			calls := 0
			_, err := c.Complete(context.Background(), "system", nil, nil, 0, func(context.Context, string, ai.Model, *ai.Request) (CompletionFinish, error) {
				calls++
				if nilFinish {
					return nil, nil
				}
				return nil, &ai.Error{Kind: ai.KindOverloaded, Message: "deny"}
			})
			var ae *ai.AdmissionError
			if !errors.As(err, &ae) || calls != 1 || p.driver.Last() != nil {
				t.Fatalf("err=%v admissions=%d dispatched=%v", err, calls, p.driver.Last())
			}
		})
	}
}

func TestCompletionRecordingFinishFailureNotRetried(t *testing.T) {
	p := &mockLLMProvider{driver: aitest.New(aitest.Says("answer"))}
	c := NewClient(p, "model", 100)
	finishes := 0
	failure := &ai.Error{Kind: ai.KindOverloaded, Message: "persistence"}
	resp, err := c.Complete(context.Background(), "system", nil, nil, 0, func(_ context.Context, name string, _ ai.Model, req *ai.Request) (CompletionFinish, error) {
		if name != "mock" || req.System != "system" {
			t.Fatalf("wrong prepared request: %s %#v", name, req)
		}
		return func(ctx context.Context, r *ai.Response, callErr error) error {
			finishes++
			if ctx.Err() != nil || r == nil || callErr != nil {
				t.Errorf("invalid finish: %v %v %v", ctx.Err(), r, callErr)
			}
			return failure
		}, nil
	})
	var re *RecordingError
	if !errors.As(err, &re) || !errors.Is(err, failure) || finishes != 1 || resp.Content.Text() != "answer" {
		t.Fatalf("resp=%+v err=%v finishes=%d", resp, err, finishes)
	}
}

func TestCompletionRecordingPerRetry(t *testing.T) {
	p := &mockLLMProvider{driver: aitest.New(aitest.Fails(&ai.Error{Kind: ai.KindOverloaded, Message: "retry"}), aitest.Replies(ai.Response{Content: ai.TextContent("ok"), Usage: ai.Usage{Input: 7, Output: 3}}))}
	c := NewClient(p, "model", 100)
	begins, finishes := 0, 0
	_, err := c.Complete(context.Background(), "", nil, nil, 0, func(context.Context, string, ai.Model, *ai.Request) (CompletionFinish, error) {
		begins++
		attempt := begins
		return func(_ context.Context, r *ai.Response, err error) error {
			finishes++
			if attempt == 1 && err == nil {
				t.Error("lost provider error")
			}
			if attempt == 2 && (err != nil || r == nil || r.Usage.Input != 7) {
				t.Errorf("lost success usage: %+v %v", r, err)
			}
			return nil
		}, nil
	})
	if err != nil || begins != 2 || finishes != 2 {
		t.Fatalf("err=%v begins=%d finishes=%d", err, begins, finishes)
	}
}

func TestCompletionRecordingConcurrentPerCall(t *testing.T) {
	p := &mockLLMProvider{driver: aitest.New(aitest.Says("one"), aitest.Says("two"))}
	c := NewClient(p, "model", 100)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			count := 0
			_, err := c.Complete(context.Background(), "", nil, nil, 0, func(context.Context, string, ai.Model, *ai.Request) (CompletionFinish, error) {
				return func(context.Context, *ai.Response, error) error { count++; return nil }, nil
			})
			if err != nil || count != 1 {
				t.Errorf("err=%v own finishes=%d", err, count)
			}
		}()
	}
	wg.Wait()
}

func TestCompletionRecordingCancelledPersistenceContext(t *testing.T) {
	p := &mockLLMProvider{driver: aitest.New(aitest.Says("answer"))}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "recording-test", "value"))
	defer cancel()
	finishes := 0
	_, _ = Complete(ctx, p, CompletionOptions{Admission: func(context.Context, string, ai.Model, *ai.Request) (CompletionFinish, error) {
		cancel()
		return func(persist context.Context, _ *ai.Response, _ error) error {
			finishes++
			if persist.Err() != nil || persist.Value("recording-test") != "value" {
				t.Errorf("persistence context lost lifetime or values: %v", persist.Err())
			}
			return nil
		}, nil
	}})
	if finishes != 1 {
		t.Fatalf("finishes=%d", finishes)
	}
}

func TestCompletionRecordingRejectsMultipleAdmissions(t *testing.T) {
	c := NewClient(nil, "model", 100)
	if _, err := c.Complete(context.Background(), "", nil, nil, 0, nil, nil); err == nil {
		t.Fatal("accepted multiple admissions")
	}
}
