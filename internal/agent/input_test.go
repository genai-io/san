package agent

import (
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestInputQueueDeliveryRetainsRejectedInput(t *testing.T) {
	for _, failAt := range []int{0, 1, 2} {
		t.Run([]string{"first", "middle", "last"}[failAt], func(t *testing.T) {
			q := InputQueue{
				{Message: core.UserMessage("user", nil), FromUser: true},
				{Message: core.UserMessage("notice", nil)},
				{Message: core.UserMessage("next user", nil), FromUser: true},
			}
			for i, id := range []string{"user", "notice", "next-user"} {
				q[i].ID = id
			}
			original := append(InputQueue(nil), q...)
			rejected := errors.New("runtime unavailable")
			var accepted []string
			var recalled []string
			var order []string
			prepare := func(msg core.Message) core.Message {
				order = append(order, "prepare:"+msg.ID)
				msg.Content = append(msg.Content, core.UserMessage("reminder", nil).Content...)
				return msg
			}
			recall := func(text string) {
				order = append(order, "recall:"+text)
				recalled = append(recalled, text)
			}
			send := func(msg core.Message) error {
				order = append(order, "send:"+msg.ID)
				if len(accepted) == failAt {
					return rejected
				}
				accepted = append(accepted, msg.ID)
				return nil
			}
			if err := q.Deliver(send, prepare, recall); !errors.Is(err, rejected) {
				t.Fatalf("delivery error = %v", err)
			}
			if len(q) != 3-failAt || q[0].ID != original[failAt].ID || len(q[0].Content) != 2 {
				t.Fatalf("rejected prepared input not retained: %+v", q)
			}
			for i := failAt + 1; i < len(original); i++ {
				if !reflect.DeepEqual(q[i-failAt], original[i]) {
					t.Fatal("delivery touched input after rejection")
				}
			}
			var wantOrder, wantRecall []string
			for i := 0; i <= failAt; i++ {
				if original[i].FromUser {
					wantOrder = append(wantOrder, "recall:"+original[i].Text())
					wantRecall = append(wantRecall, original[i].Text())
				}
				wantOrder = append(wantOrder, "prepare:"+original[i].ID, "send:"+original[i].ID)
			}
			if !reflect.DeepEqual(order, wantOrder) || !reflect.DeepEqual(recalled, wantRecall) {
				t.Fatalf("order=%v recall=%v", order, recalled)
			}
			retained := q[0].Message
			if err := q.Deliver(func(msg core.Message) error {
				if len(accepted) == failAt && !reflect.DeepEqual(msg, retained) {
					t.Fatal("retry lost prepared native message")
				}
				accepted = append(accepted, msg.ID)
				return nil
			}, func(msg core.Message) core.Message { return msg }, recall); err != nil {
				t.Fatal(err)
			}
			if len(q) != 0 || !reflect.DeepEqual(accepted, []string{original[0].ID, original[1].ID, original[2].ID}) {
				t.Fatalf("retry duplicated or reordered input: %v", accepted)
			}
		})
	}
}

func TestEmptyInputQueueDoesNotCallDelivery(t *testing.T) {
	var q InputQueue
	if err := q.Deliver(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}
