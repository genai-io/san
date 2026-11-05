package broker

import (
	"reflect"
	"sync"
	"testing"
)

func completionMessage(task string, attempt int) Message {
	return Message{To: "owner", From: task, SessionID: "session", Subject: "done", Content: "result " + task,
		TaskCompletion: &TaskCompletion{TaskID: task, Attempt: attempt}}
}

func TestCompletionRetainsOrderedDistinctResults(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register(Main, func(Message) bool { t.Error("completion must not reach Main"); return true })
	first := completionMessage("task-a", 1)
	second := completionMessage("task-b", 1)
	nextAttempt := completionMessage("task-a", 2)
	otherSession := first
	otherSession.SessionID = "other-session"
	want := []Message{first, second, nextAttempt, otherSession}
	for _, m := range want {
		if !SendCompletion(m) || !SendCompletion(m) {
			t.Fatal("completion should be retained, including pending duplicates")
		}
	}
	var got []Message
	Register("owner", func(m Message) bool { got = append(got, m); return true })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered = %#v, want %#v", got, want)
	}
	Register("owner", func(Message) bool { t.Error("accepted mail delivered twice"); return true })
}

func TestCompletionRejectsInvalidMailAndOrdinarySendDoesNotQueue(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	for _, m := range []Message{{To: "owner"}, {TaskCompletion: &TaskCompletion{}}, {To: Main, TaskCompletion: &TaskCompletion{}}} {
		if SendCompletion(m) {
			t.Fatalf("invalid completion accepted: %#v", m)
		}
	}
	if Send(completionMessage("ordinary-send", 1)) {
		t.Fatal("ordinary Send to absent owner must fail even with completion metadata")
	}
	Register("owner", func(Message) bool { return false })
	if Send(Message{To: "owner", Content: "ordinary"}) {
		t.Fatal("ordinary Send must report receiver rejection")
	}
	Register("owner", func(Message) bool { t.Error("ordinary or invalid mail was queued"); return true })
	var mainCalls int
	Register(Main, func(Message) bool { mainCalls++; return true })
	if !Send(Message{To: Main}) || mainCalls != 1 {
		t.Fatal("ordinary Main delivery changed")
	}
}

func TestCompletionRejectedThenReregistered(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register("owner", func(Message) bool { return false })
	m := completionMessage("task", 1)
	if !SendCompletion(m) {
		t.Fatal("rejected completion should be retained")
	}
	Unregister("owner")
	var got []Message
	Register("owner", func(m Message) bool { got = append(got, m); return true })
	if !reflect.DeepEqual(got, []Message{m}) {
		t.Fatalf("delivered = %#v, want retained completion", got)
	}
}

func TestCompletionRegistrationRacesRejectedDelivery(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	Register("owner", func(Message) bool {
		close(entered)
		<-release // Deliberately pause a callback to exercise registration interleaving.
		return false
	})
	first, second := completionMessage("first", 1), completionMessage("second", 1)
	go func() { defer close(done); SendCompletion(first) }()
	<-entered
	SendCompletion(first) // In-flight duplicates also coalesce.
	SendCompletion(second)
	Unregister("owner")
	var got []Message
	Register("owner", func(m Message) bool { got = append(got, m); return true })
	close(release)
	<-done
	if !reflect.DeepEqual(got, []Message{first, second}) {
		t.Fatalf("delivered = %#v, want both in order", got)
	}
}

func TestCompletionCallbackCanReenterBroker(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	first, second := completionMessage("first", 1), completionMessage("second", 1)
	var got []Message
	Register("owner", func(m Message) bool {
		SendCompletion(second)
		Unregister("owner")
		Register("owner", func(m Message) bool { got = append(got, m); return true })
		return false
	})
	SendCompletion(first)
	if !reflect.DeepEqual(got, []Message{first, second}) {
		t.Fatalf("reentrant delivery = %#v", got)
	}
}

func TestCompletionConcurrentSendAndRegister(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	for attempt := 0; attempt < 100; attempt++ {
		Unregister("owner")
		m := completionMessage("task", attempt)
		var wg sync.WaitGroup
		var received int // Only one completion drainer invokes this receiver.
		wg.Add(2)
		go func() { defer wg.Done(); SendCompletion(m) }()
		go func() {
			defer wg.Done()
			Register("owner", func(Message) bool { received++; return true })
		}()
		wg.Wait()
		if received != 1 {
			t.Fatalf("attempt %d received %d times", attempt, received)
		}
	}
}

func TestCompletionResetDiscardsPendingAndInflightRejection(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	SendCompletion(completionMessage("pending", 1))
	Reset()
	Register("owner", func(Message) bool {
		Reset()
		Register("owner", func(Message) bool { t.Error("Reset resurrected completion"); return true })
		return false
	})
	SendCompletion(completionMessage("in-flight", 1))
	Register("owner", func(Message) bool { t.Error("Reset retained completion"); return true })
}

func TestCompletionRetainsItsOwnMetadata(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	m := completionMessage("task", 1)
	SendCompletion(m)
	m.TaskCompletion.Attempt = 99
	Register("owner", func(m Message) bool { m.TaskCompletion.Attempt = 100; return false })
	Register("owner", func(m Message) bool {
		if m.TaskCompletion.Attempt != 1 {
			t.Fatalf("retained metadata mutated: %#v", m.TaskCompletion)
		}
		return true
	})
}
