package runtime

import (
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func drainInput(q *InputQueue, send func(PendingInput) error, prepare func(core.Message) core.Message) error {
	for q.CanDeliver(false) && q.Len() > 0 {
		if err := q.DeliverFirst(send, prepare); err != nil {
			return err
		}
	}
	return nil
}

func TestInputQueueDeliveryRetainsRejectedInput(t *testing.T) {
	for _, failAt := range []int{0, 1, 2} {
		t.Run([]string{"first", "middle", "last"}[failAt], func(t *testing.T) {
			q := InputQueue{items: []PendingInput{
				{Message: core.UserMessage("user", nil), FromUser: true},
				{Message: core.UserMessage("notice", nil)},
				{Message: core.UserMessage("next user", nil), FromUser: true},
			}}
			for i, id := range []string{"user", "notice", "next-user"} {
				q.items[i].ID = id
			}
			original := append([]PendingInput(nil), q.items...)
			rejected := errors.New("runtime unavailable")
			var accepted []string
			var order []string
			prepare := func(msg core.Message) core.Message {
				order = append(order, "prepare:"+msg.ID)
				msg.Content = append(msg.Content, core.UserMessage("reminder", nil).Content...)
				return msg
			}
			send := func(msg PendingInput) error {
				order = append(order, "send:"+msg.ID)
				if len(accepted) == failAt {
					return rejected
				}
				accepted = append(accepted, msg.ID)
				return nil
			}
			if err := drainInput(&q, send, prepare); !errors.Is(err, rejected) {
				t.Fatalf("delivery error = %v", err)
			}
			if q.Len() != 3-failAt || q.items[0].ID != original[failAt].ID || len(q.items[0].Content) != 2 {
				t.Fatalf("rejected prepared input not retained: %+v", q)
			}
			for i := failAt + 1; i < len(original); i++ {
				if !reflect.DeepEqual(q.items[i-failAt], original[i]) {
					t.Fatal("delivery touched input after rejection")
				}
			}
			var wantOrder []string
			for i := 0; i <= failAt; i++ {
				wantOrder = append(wantOrder, "prepare:"+original[i].ID, "send:"+original[i].ID)
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("order=%v, want %v", order, wantOrder)
			}
			retained := q.items[0].Message
			if err := drainInput(&q, func(msg PendingInput) error {
				if len(accepted) == failAt && !reflect.DeepEqual(msg.Message, retained) {
					t.Fatal("retry lost prepared native message")
				}
				accepted = append(accepted, msg.ID)
				return nil
			}, func(msg core.Message) core.Message { return msg }); err != nil {
				t.Fatal(err)
			}
			if q.Len() != 0 || !reflect.DeepEqual(accepted, []string{original[0].ID, original[1].ID, original[2].ID}) {
				t.Fatalf("retry duplicated or reordered input: %v", accepted)
			}
		})
	}
}

func TestEmptyInputQueueDoesNotCallDelivery(t *testing.T) {
	var q InputQueue
	if err := q.DeliverFirst(nil, nil); err != nil {
		t.Fatal(err)
	}
}
