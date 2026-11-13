package core

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func compactionTestAgent(t *testing.T, cfg Config) *agent {
	t.Helper()
	cfg.System = NewSystem()
	cfg.Tools = NewTools()
	if cfg.Client == nil {
		cfg.Client = testClient(aitest.Always(talks("done")))
	}
	a := NewAgent(cfg).(*agent)
	// Tests observe synchronously through OnEvent, avoiding outbox backpressure.
	a.outbox = nil
	a.SetMessages([]Message{UserMessage("one", nil), AssistantMessage("two", "", nil), UserMessage("three", nil)})
	return a
}

func TestAcceptedCompactionCanceledWithFullOutboxUnwinds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	boundary := make(chan struct{})
	addedCount, boundaryCount := 0, 0
	a := compactionTestAgent(t, Config{OnEvent: func(e Event) {
		switch e.(type) {
		case sdkagent.MessageAdded:
			addedCount++
		case Compacted:
			boundaryCount++
			close(boundary)
		}
	}})
	a.outbox = make(chan Event, 1)
	a.outbox <- AgentStarted{}
	done := make(chan error, 1)
	go func() {
		done <- a.applyCompaction(ctx, Compaction{Summary: "accepted", Finish: func(context.Context, Message) error { cancel(); return nil }}, 3, "manual")
	}()
	// Leave the transport full until the first critical event's bounded send
	// expires. Reaching the second callback proves cancellation cannot trap the
	// accepted compaction indefinitely behind an absent consumer.
	select {
	case <-boundary:
	case <-time.After(8 * time.Second):
		t.Fatal("full outbox prevented bounded unwind")
	}
	<-a.outbox // release the consumer for the boundary event
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("compaction did not unwind after consumer release")
	}
	if addedCount != 1 || boundaryCount != 1 {
		t.Fatalf("callbacks: added=%d boundary=%d", addedCount, boundaryCount)
	}
	if msgs := a.Messages(); len(msgs) != 1 || msgs[0].Text() != FormatCompactSummary("accepted") {
		t.Fatalf("accepted history = %+v", msgs)
	}
	if _, ok := (<-a.outbox).(Compacted); !ok {
		t.Fatal("missing released boundary event")
	}
}

func TestCompactionCancellationInsideFinishKeepsAcceptedBoundary(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "automatic"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			llm := aitest.Always(talks("must not dispatch"))
			var committed Message
			closed := 0
			result := Compaction{Summary: "accepted", Finish: func(_ context.Context, msg Message) error {
				committed = msg
				cancel()
				return nil
			}, Close: func() error { closed++; return nil }}
			a := compactionTestAgent(t, Config{Client: testClient(llm), ContextBudget: func() int { return 1 }, CompactFunc: func(context.Context, []Message) (Compaction, error) { return result, nil }})
			// Keep a buffered outbox to verify transport publication, not only OnEvent.
			a.outbox = make(chan Event, 32)
			if manual {
				if err := a.applyCompaction(ctx, result, 3, "manual"); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err := a.ThinkAct(ctx)
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			msgs := a.Messages()
			if len(msgs) != 1 || msgs[0].ID != committed.ID || msgs[0].Text() != FormatCompactSummary("accepted") {
				t.Fatalf("accepted history = %+v; committed = %+v", msgs, committed)
			}
			added, boundary := false, false
			for len(a.outbox) > 0 {
				switch e := (<-a.outbox).(type) {
				case sdkagent.MessageAdded:
					if e.Message.ID == committed.ID {
						added = true
					}
				case Compacted:
					if e.SummaryMessageID == committed.ID {
						boundary = true
					}
				}
			}
			if !added || !boundary || closed != 0 || llm.Calls() != 0 {
				t.Fatalf("added=%v boundary=%v closed=%d dispatches=%d", added, boundary, closed, llm.Calls())
			}
		})
	}
}

func TestCompactionSuccessfulFinishIsTerminalBeforePublication(t *testing.T) {
	var committed Message
	closed := false
	events := 0
	a := compactionTestAgent(t, Config{OnEvent: func(e Event) {
		switch e.(type) {
		case sdkagent.MessageAdded, Compacted:
			events++
			if committed.ID == "" || closed {
				t.Fatal("published without successful terminal Finish")
			}
		}
	}})
	err := a.applyCompaction(context.Background(), Compaction{Summary: "raw summary", Finish: func(_ context.Context, m Message) error {
		if len(a.Messages()) != 3 {
			t.Fatal("replaced before commit")
		}
		committed = m
		return nil
	}, Close: func() error { closed = true; return errors.New("must not clean up committed scope") }}, 3, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if events != 2 || a.Messages()[0].ID != committed.ID || committed.Text() != FormatCompactSummary("raw summary") {
		t.Fatalf("incorrect committed boundary: %+v", committed)
	}
}

func TestCompactionPersistenceFailurePreventsDispatch(t *testing.T) {
	for _, stage := range []string{"finish", "close", "admission"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("persist failed")
			llm := aitest.Always(talks("unexpected"))
			closed, published := 0, 0
			a := compactionTestAgent(t, Config{Client: testClient(llm), ContextBudget: func() int { return 1 },
				CompactFunc: func(context.Context, []Message) (Compaction, error) {
					c := Compaction{Summary: "summary", Close: func() error {
						closed++
						if stage == "close" {
							return failure
						}
						return nil
					}}
					if stage == "finish" {
						c.Finish = func(context.Context, Message) error { return failure }
					}
					if stage == "admission" {
						return c, &ai.AdmissionError{Err: failure}
					}
					return c, nil
				}, OnEvent: func(e Event) {
					switch e.(type) {
					case sdkagent.MessageAdded, Compacted:
						published++
					}
				},
			})
			_, err := a.ThinkAct(context.Background())
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if closed != 1 || published != 0 || llm.Calls() != 0 || len(a.Messages()) != 3 {
				t.Fatalf("closed=%d published=%d calls=%d history=%d", closed, published, llm.Calls(), len(a.Messages()))
			}
		})
	}
}

func TestCompactionCancellationDiscardsResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closed, finished, published := 0, 0, 0
	a := compactionTestAgent(t, Config{CompactFunc: func(context.Context, []Message) (Compaction, error) {
		cancel()
		return Compaction{Summary: "discard", Finish: func(context.Context, Message) error { finished++; return nil }, Close: func() error { closed++; return nil }}, nil
	}, OnEvent: func(e Event) {
		switch e.(type) {
		case sdkagent.MessageAdded, Compacted:
			published++
		}
	}})
	msgs, err := a.shorten(ctx, a.Messages(), "auto")
	if !errors.Is(err, context.Canceled) || msgs != nil || closed != 1 || finished != 0 || published != 0 {
		t.Fatalf("msgs=%v err=%v closed=%d finished=%d published=%d", msgs, err, closed, finished, published)
	}
}

func TestShutdownClosesQueuedCompactionsAndReportsError(t *testing.T) {
	failure := errors.New("discard accounting failed")
	closed := 0
	var stopped error
	a := compactionTestAgent(t, Config{InboxBuf: 2, OnEvent: func(e Event) {
		if e, ok := e.(AgentStopped); ok {
			stopped = e.Err
		}
	}})
	a.Inbox() <- Inbound{Signal: SigStop}
	if !a.Compact(Compaction{Summary: "queued", Close: func() error { closed++; return failure }}) {
		t.Fatal("rejected queued compaction")
	}
	if a.Compact(Compaction{}) {
		t.Fatal("accepted into full inbox")
	}
	err := a.Run(context.Background())
	if !errors.Is(err, failure) || !errors.Is(stopped, failure) || closed != 1 {
		t.Fatalf("run=%v stopped=%v closed=%d", err, stopped, closed)
	}
	if a.Compact(Compaction{}) {
		t.Fatal("accepted after shutdown")
	}
}

func TestCompactionProviderErrorClosesAndFailsOpen(t *testing.T) {
	closed := 0
	llm := aitest.Always(talks("done"))
	a := compactionTestAgent(t, Config{MaxSteps: 1, Client: testClient(llm), ContextBudget: func() int { return 1 }, CompactFunc: func(context.Context, []Message) (Compaction, error) {
		return Compaction{Close: func() error { closed++; return nil }}, errors.New("provider failed")
	}})
	if _, err := a.ThinkAct(context.Background()); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || llm.Calls() != 1 {
		t.Fatalf("closed=%d calls=%d", closed, llm.Calls())
	}
}

func TestManualCompactionFailureStopsMailboxWithoutMutation(t *testing.T) {
	failure := errors.New("finish failed")
	closed, published := 0, 0
	a := compactionTestAgent(t, Config{InboxBuf: 1, OnEvent: func(e Event) {
		switch e.(type) {
		case sdkagent.MessageAdded, Compacted:
			published++
		}
	}})
	if !a.Compact(Compaction{Summary: "summary", Finish: func(context.Context, Message) error { return failure }, Close: func() error { closed++; return nil }}) {
		t.Fatal("rejected")
	}
	if err := a.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Run = %v", err)
	}
	if closed != 1 || published != 0 || len(a.Messages()) != 3 {
		t.Fatalf("closed=%d published=%d history=%d", closed, published, len(a.Messages()))
	}
}

func TestCompactionRecoveryPropagatesPersistenceError(t *testing.T) {
	failure := errors.New("finish failed")
	a := compactionTestAgent(t, Config{CompactFunc: func(context.Context, []Message) (Compaction, error) {
		return Compaction{Summary: "summary", Finish: func(context.Context, Message) error { return failure }}, nil
	}})
	recovery, err := a.onInferError(context.Background(), sdkagent.InferErrorContext{Messages: a.Messages(), Err: &ai.Error{Kind: ai.KindContextExceeded}})
	if recovery != nil || !errors.Is(err, failure) {
		t.Fatalf("recovery=%v err=%v", recovery, err)
	}
}
