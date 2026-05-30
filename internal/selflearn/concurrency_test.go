package selflearn

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/gen-code/internal/core"
)

// TestSnapshotIsCopiedBeforeGoroutine guards the defensive copy in Observe:
// the goroutine handed `snapshot` must see the slice as it was at trigger
// time, even if the caller mutates result.Messages afterwards (the main
// agent loop reuses its message slice).
//
// The test passes a slice with one message, fires the review (memory arm
// at interval 1), mutates the slice to a different content while the
// ReviewFunc is still running, and asserts the ReviewFunc saw the original.
func TestSnapshotIsCopiedBeforeGoroutine(t *testing.T) {
	type guarded struct {
		mu    sync.Mutex
		seen  string
		fired chan struct{}
	}
	g := &guarded{fired: make(chan struct{})}

	review := func(_ ReviewKind, snapshot []core.Message) {
		g.mu.Lock()
		if len(snapshot) > 0 {
			g.seen = snapshot[0].Content
		}
		g.mu.Unlock()
		close(g.fired)
	}
	r := New(Config{Memory: Arm{Enabled: true, Interval: 1}}, review)

	original := []core.Message{{Role: core.RoleUser, Content: "ORIGINAL"}}
	r.Observe(core.Result{
		StopReason: core.StopEndTurn,
		Messages:   original,
	})

	// Mutate the caller-owned slice — the goroutine's snapshot must NOT alias
	// it, so the ReviewFunc must see "ORIGINAL", not "MUTATED".
	original[0].Content = "MUTATED"

	select {
	case <-g.fired:
	case <-time.After(time.Second):
		t.Fatal("review never fired")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen != "ORIGINAL" {
		t.Fatalf("snapshot leak: review saw %q, want %q", g.seen, "ORIGINAL")
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
