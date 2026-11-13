package agent

import (
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestInputQueueCancellationAdmission(t *testing.T) {
	for _, continueBeforeCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "continue after cleanup", true: "continue before cleanup"}[continueBeforeCleanup], func(t *testing.T) {
			var q InputQueue
			receipt := &core.TurnFinalization{}
			if !q.Hold(receipt) || q.Hold(receipt) || q.Hold(nil) {
				t.Fatal("cancellation waiter was missing or duplicated")
			}
			q.Enqueue(PendingInput{Message: core.UserMessage("background", nil)})
			user := PendingInput{Message: core.UserMessage("continue", nil), FromUser: true}
			if continueBeforeCleanup {
				q.Enqueue(user)
			}
			if q.CanDeliver(false) || q.CanDeliver(true) {
				t.Fatal("continuation crossed consumer cleanup fence")
			}
			// Delivery itself enforces admission, even if an adapter forgets its preflight.
			if err := q.Deliver(nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if q.Finalize(nil) || q.Finalize(&core.TurnFinalization{}) {
				t.Fatal("unrelated completion released the fence")
			}
			if !q.Finalize(receipt) || q.Finalize(receipt) {
				t.Fatal("matching completion must release exactly once")
			}
			if !continueBeforeCleanup {
				if q.CanDeliver(false) || !q.CanDeliver(true) || !q.Held() {
					t.Fatal("cleanup granted automatic continuation")
				}
				if err := q.Deliver(nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				q.Enqueue(user)
			}
			var order []string
			if err := q.Deliver(func(msg PendingInput) error {
				order = append(order, "send:"+msg.Text())
				return nil
			}, func(msg core.Message) core.Message {
				order = append(order, "prepare:"+msg.Text())
				return msg
			}, func(text string) error { order = append(order, "recall:"+text); return nil }); err != nil {
				t.Fatal(err)
			}
			want := []string{"prepare:background", "send:background", "recall:continue", "prepare:continue", "send:continue"}
			if !reflect.DeepEqual(order, want) || q.Len() != 0 || q.Held() {
				t.Fatalf("continuation lost FIFO or recall policy: %v", order)
			}
		})
	}
}

func TestInputQueueRepeatedStopRevokesContinuation(t *testing.T) {
	var q InputQueue
	first, second := &core.TurnFinalization{}, &core.TurnFinalization{}
	q.Hold(first)
	q.Enqueue(PendingInput{Message: core.UserMessage("continue", nil), FromUser: true})
	q.Hold(second)
	if q.Finalize(first) || q.CanDeliver(true) || !q.Held() {
		t.Fatal("old completion released a newer cancellation")
	}
	q.Finalize(second)
	if err := q.Deliver(nil, nil, nil); err != nil || q.Len() != 1 {
		t.Fatal("second stop failed to retain continuation")
	}
}

func TestInputQueueDiscardAndRuntimeReset(t *testing.T) {
	var q InputQueue
	receipt := &core.TurnFinalization{}
	q.Enqueue(PendingInput{Message: core.UserMessage("old session", nil)})
	q.Hold(receipt)
	q.Discard()
	if q.Len() != 0 || q.Cancelling() != nil || !q.Held() || q.Finalize(receipt) {
		t.Fatal("discard retained old input/fence or granted continuation")
	}
	q.Enqueue(PendingInput{Message: core.UserMessage("pending retry", nil)})
	q.Hold(receipt)
	q.ResetAdmission()
	if !q.CanDeliver(false) || q.Cancelling() != nil || q.First().Text() != "pending retry" {
		t.Fatal("runtime reset lost retry input or retained cancellation state")
	}
}
