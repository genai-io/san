package core

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestCancelledOutputPrecheckFinalizesThroughActualConsumer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	captured := make(chan *TurnFinalization, 1)
	terminal := make(chan Result, 1)
	var ag Agent
	ag = NewAgent(Config{Client: testClient(aitest.New(aitest.Says("unpublished"))), System: NewSystem(), Tools: NewTools(), OutputAdmission: func(context.Context, sdkagent.OutputAdmission) error {
		t.Error("output admission ran after cancelled precheck")
		return nil
	}, OnEvent: func(event Event) {
		if end, ok := event.(sdkagent.InferenceEnd); ok && end.Err == nil {
			captured <- ag.Cancel()
		}
	}})
	done := make(chan error, 1)
	go func() { done <- ag.Run(ctx) }()
	go func() {
		for event := range ag.Outbox() {
			if end, ok := event.(TurnEnded); ok {
				terminal <- end.Result
			}
		}
	}()
	ag.Append(UserMessage("go", nil))
	var receipt *TurnFinalization
	select {
	case receipt = <-captured:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if receipt == nil {
		t.Fatal("no captured execution")
	}
	select {
	case end := <-terminal:
		if end.StopReason != StopCanceled || end.Finalization != receipt || end.StopDetail != context.Canceled.Error() {
			t.Fatalf("wrong direct-cancel wrapper outcome: %+v", end)
		}
		end.Finalization.Acknowledge(nil)
	case <-ctx.Done():
		t.Fatal("prepublication cancellation did not emit terminal")
	}
	if err := receipt.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for _, message := range ag.Messages() {
		if message.Role == "assistant" {
			t.Fatal("unadmitted output published")
		}
	}
	cancel()
	<-done
}

func TestCancelledOutputAdmissionFinalizesThroughActualConsumer(t *testing.T) {
	for _, joinedFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancellation-only", true: "joined-storage-failure"}[joinedFailure], func(t *testing.T) {
			entered := make(chan struct{})
			writeErr := errors.New("output storage failed")
			ag := NewAgent(Config{Client: testClient(aitest.New(aitest.Says("unpublished"))), System: NewSystem(), Tools: NewTools(), OutputAdmission: func(ctx context.Context, _ sdkagent.OutputAdmission) error {
				close(entered)
				<-ctx.Done()
				if joinedFailure {
					return errors.Join(ctx.Err(), writeErr)
				}
				return ctx.Err()
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- ag.Run(ctx) }()
			terminals := make(chan Result, 1)
			go func() {
				for event := range ag.Outbox() {
					if end, ok := event.(TurnEnded); ok {
						terminals <- end.Result
					}
				}
			}()
			ag.Append(UserMessage("go", nil))
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			receipt := ag.Cancel()
			if receipt == nil {
				t.Fatal("missing cancellation receipt")
			}
			if joinedFailure {
				select {
				case err := <-done:
					if !errors.Is(err, writeErr) {
						t.Fatalf("storage failure was swallowed as cancellation: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("storage failure did not stop runtime")
				}
				if err := receipt.Wait(ctx); !errors.Is(err, writeErr) {
					t.Fatalf("receipt lost storage error: %v", err)
				}
				select {
				case <-terminals:
					t.Fatal("failed storage fabricated cancelled terminal")
				default:
				}
			} else {
				var terminal Result
				select {
				case terminal = <-terminals:
				case <-ctx.Done():
					t.Fatal("missing actual cancellation terminal")
				}
				if terminal.StopReason != StopCanceled || terminal.Finalization != receipt {
					t.Fatalf("wrong terminal/receipt: %+v", terminal)
				}
				short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
				if err := receipt.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("receipt bypassed terminal consumer: %v", err)
				}
				stop()
				terminal.Finalization.Acknowledge(nil)
				if err := receipt.Wait(ctx); err != nil {
					t.Fatalf("clean cancellation failed finalization: %v", err)
				}
				cancel()
				<-done
			}
			for _, message := range ag.Messages() {
				if message.Role == "assistant" {
					t.Fatal("rejected output was published")
				}
			}
		})
	}
}
