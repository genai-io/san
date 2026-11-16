package broker

import (
	"reflect"
	"testing"
)

func TestRetryCompletionsCapacityAndFIFO(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	first, second := completionMessage("first", 1), completionMessage("second", 1)
	full := true
	var got []Message
	cleanup := RegisterAccept("owner", func(m Message) error {
		if full {
			return ErrInboxFull
		}
		got = append(got, m)
		return nil
	})
	SendCompletion(first)
	SendCompletion(second)
	SendCompletion(first) // Pending duplicates remain coalesced across a wake.
	full = false
	RetryCompletions("owner")
	RetryCompletions("owner")
	if !reflect.DeepEqual(got, []Message{first, second}) {
		t.Fatalf("delivered = %#v", got)
	}
	// Capacity retries must not replace the registration or break its cleanup.
	cleanup()
	if Send(Message{To: "owner"}) {
		t.Fatal("retry replaced scoped registration")
	}
}

func TestRetryCompletionsDuringInflightRejection(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, second := completionMessage("first", 1), completionMessage("second", 1)
	calls := 0
	var got []Message
	RegisterAccept("owner", func(m Message) error {
		calls++
		if calls == 1 {
			close(entered)
			<-release
			return ErrInboxFull // The capacity wake precedes this stale rejection.
		}
		got = append(got, m)
		return nil
	})
	go func() { defer close(done); SendCompletion(first) }()
	<-entered
	SendCompletion(first)
	SendCompletion(second)
	RetryCompletions("owner")
	RetryCompletions("owner") // Coalesce wakes; never run a second callback concurrently.
	close(release)
	<-done
	if calls != 3 || !reflect.DeepEqual(got, []Message{first, second}) {
		t.Fatalf("calls = %d, delivered = %#v", calls, got)
	}
}

func TestRetryCompletionsReentrantWakeConsumedOnce(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	calls := 0
	RegisterAccept("owner", func(Message) error {
		calls++
		if calls == 1 {
			RetryCompletions("owner")
		}
		return ErrInboxFull
	})
	SendCompletion(completionMessage("task", 1))
	if calls != 2 {
		t.Fatalf("calls = %d, want one retry", calls)
	}
	RetryCompletions("owner")
	if calls != 3 {
		t.Fatalf("calls = %d, want fresh wake retry", calls)
	}
}

func TestRetryCompletionsNoFallbackStartupOrOrdinaryMail(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register(Main, func(Message) bool { t.Error("retry fell back to Main"); return true })
	RetryCompletions("unknown")
	if Send(Message{To: "unknown"}) {
		t.Fatal("retry registered absent owner")
	}
	SendCompletion(completionMessage("retained", 1))
	RetryCompletions("owner")
	if Send(Message{To: "owner"}) {
		t.Fatal("retry started absent owner")
	}
	calls := 0
	cleanup := Register("ordinary", func(Message) bool { calls++; return false })
	Send(Message{To: "ordinary", Content: "not retained"})
	RetryCompletions("ordinary")
	if calls != 1 {
		t.Fatalf("ordinary delivery calls = %d", calls)
	}
	cleanup()
	var got []Message
	Register("owner", func(m Message) bool { got = append(got, m); return true })
	if !reflect.DeepEqual(got, []Message{completionMessage("retained", 1)}) {
		t.Fatalf("absent retry lost mail: %#v", got)
	}
}

func TestRetryCompletionsResetAndReplacementDuringWake(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	oldCleanup := RegisterAccept("owner", func(Message) error {
		RetryCompletions("owner")
		Reset()
		Register("owner", func(m Message) bool {
			if m.TaskCompletion != nil {
				t.Error("detached queue resurrected")
			}
			return true
		})
		return ErrInboxFull
	})
	SendCompletion(completionMessage("old", 1))
	oldCleanup()
	RetryCompletions("owner")
	if !Send(Message{To: "owner"}) {
		t.Fatal("old cleanup removed replacement")
	}
}
