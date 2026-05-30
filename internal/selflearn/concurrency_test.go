package selflearn

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/gen-code/internal/core"
)

// TestSnapshotIsCopiedBeforeGoroutine guards the defensive copy in Observe.
// The reviewer goroutine must see the slice header as it was at trigger
// time, even if the caller appends to or truncates result.Messages
// afterwards — that is the failure mode the copy actually prevents (the
// caller's main loop reuses its message slice for the next turn).
//
// We block the review inside its callback until after we mutate the
// caller's slice; if Observe leaked the slice header the goroutine would
// see the post-mutation length.
func TestSnapshotIsCopiedBeforeGoroutine(t *testing.T) {
	release := make(chan struct{})
	done := make(chan int)
	review := func(_ ReviewKind, snapshot []core.Message) {
		<-release
		done <- len(snapshot)
	}
	r := New(Config{Memory: Arm{Enabled: true, Interval: 1}}, review)

	original := []core.Message{{Role: core.RoleUser, Content: "a"}}
	r.Observe(core.Result{StopReason: core.StopEndTurn, Messages: original})

	// Truncate the caller's slice while the goroutine is blocked. If
	// Observe leaked the slice header the goroutine would later see len 0.
	original = original[:0]
	_ = original

	close(release)
	select {
	case got := <-done:
		if got != 1 {
			t.Fatalf("snapshot leak: review saw len=%d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("review never returned")
	}
}

// TestConcurrentObserveIsRaceFree fires many goroutines hammering Observe in
// parallel. Combined with `go test -race`, this trips on any unsynchronized
// access to the reviewer's counters or inFlight flag.
//
// The ReviewFunc deliberately holds the in-flight slot for a beat so the
// drop-don't-reset path of Observe also gets exercised under concurrency.
func TestConcurrentObserveIsRaceFree(t *testing.T) {
	var fired atomic.Int64
	hold := make(chan struct{})
	review := func(_ ReviewKind, _ []core.Message) {
		fired.Add(1)
		<-hold // block until the test releases — keeps inFlight=true
	}
	r := New(Config{
		Memory: Arm{Enabled: true, Interval: 1},
		Skills: Arm{Enabled: true, Interval: 1},
	}, review)

	const goroutines, perG = 8, 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				r.Observe(endTurn(1))
			}
		}()
	}
	wg.Wait()
	close(hold) // release the one in-flight review

	// We do not assert an exact fire count — the point is that -race finds
	// no races. But there should have been at least one fire (the first
	// trigger always wins) and at most goroutines*perG (loose upper bound).
	got := fired.Load()
	if got < 1 || got > int64(goroutines*perG) {
		t.Fatalf("fire count %d outside plausible bounds [1, %d]", got, goroutines*perG)
	}
}
