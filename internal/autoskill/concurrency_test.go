package autoskill

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
)

func TestReviewCopiesOnlyLatestExchange(t *testing.T) {
	release := make(chan struct{})
	done := make(chan []core.Message, 1)
	review := func(_ SkillPermissions, snapshot []core.Message) {
		<-release
		done <- snapshot
	}
	r := NewSessionReviewer(t.Context(), Config{Skills: AllowAllSkillActions()}, review)

	original := []core.Message{core.UserMessage("previous", nil), core.UserMessage("latest", nil)}
	r.Observe(core.Result{StopReason: core.StopEndTurn, Messages: original}, false, true)
	original[1] = core.UserMessage("replacement", nil)
	close(release)
	select {
	case got := <-done:
		if len(got) != 1 || got[0].Text() != "latest" {
			t.Fatalf("review saw %+v, want only the saved latest exchange", got)
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
	fireSignal := make(chan struct{}, 1)
	review := func(_ SkillPermissions, _ []core.Message) {
		fired.Add(1)
		select {
		case fireSignal <- struct{}{}: // announce the first fire
		default:
		}
		<-hold // block until the test releases — keeps inFlight=true
	}
	r := NewSessionReviewer(t.Context(), Config{Skills: SkillPermissions{AllowCreate: true}}, review)

	const goroutines, perG = 8, 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				r.Observe(endTurn(1), false, true)
			}
		}()
	}
	wg.Wait()

	// The review that wins the in-flight slot runs in its own goroutine (see
	// Reviewer.Observe), so wait for it to actually fire before reading the
	// counter — otherwise this races the async review under load and can
	// observe 0. The first trigger always wins, so a fire is guaranteed.
	select {
	case <-fireSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("expected at least one review to fire")
	}
	close(hold) // release the in-flight review

	// We do not assert an exact fire count — the point is that -race finds no
	// races. At least one fired (waited for above); the upper bound is a loose
	// sanity check.
	got := fired.Load()
	if got < 1 || got > int64(goroutines*perG) {
		t.Fatalf("fire count %d outside plausible bounds [1, %d]", got, goroutines*perG)
	}
}
