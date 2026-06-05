package selflearn

import (
	"testing"
	"time"

	"github.com/genai-io/san/v2/internal/core"
)

func endTurn(toolUses int) core.Result {
	return core.Result{StopReason: core.StopEndTurn, ToolUses: toolUses}
}

// waitFire returns the kinds of the next fired review, or fails after timeout.
func waitFire(t *testing.T, fired <-chan ReviewKind) ReviewKind {
	t.Helper()
	select {
	case k := <-fired:
		return k
	case <-time.After(time.Second):
		t.Fatal("expected a review to fire, none did")
		return 0
	}
}

// assertNoFire fails if a review fires within a short window.
func assertNoFire(t *testing.T, fired <-chan ReviewKind) {
	t.Helper()
	select {
	case k := <-fired:
		t.Fatalf("expected no review, but %v fired", k)
	case <-time.After(80 * time.Millisecond):
	}
}

// TestReviewKindString covers the log-friendly labels surfaced to the
// wire-up's review-summary log entry.
func TestReviewKindString(t *testing.T) {
	cases := map[ReviewKind]string{
		0:                       "none",
		KindMemory:              "memory",
		KindSkills:              "skill",
		KindMemory | KindSkills: "memory+skill",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Fatalf("kind %b: got %q, want %q", k, got, want)
		}
	}
}

func TestMemoryFiresOnTurnCadence(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	r := New(Config{Memory: Arm{Enabled: true, Interval: 3}}, func(k ReviewKind, _ []core.Message) { fired <- k })

	r.Observe(endTurn(0))
	r.Observe(endTurn(0))
	assertNoFire(t, fired) // only 2 turns
	r.Observe(endTurn(0))  // 3rd turn → fire
	if k := waitFire(t, fired); !k.Has(KindMemory) || k.Has(KindSkills) {
		t.Fatalf("want memory-only, got %v", k)
	}
}

func TestSkillFiresOnToolIterThreshold(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	r := New(Config{Skills: Arm{Enabled: true, Interval: 5}}, func(k ReviewKind, _ []core.Message) { fired <- k })

	r.Observe(endTurn(2))
	r.Observe(endTurn(2))
	assertNoFire(t, fired) // 4 tool-iters < 5
	r.Observe(endTurn(1))  // 5 total → fire
	if k := waitFire(t, fired); !k.Has(KindSkills) || k.Has(KindMemory) {
		t.Fatalf("want skill-only, got %v", k)
	}
}

func TestCombinedFiresBothArms(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	r := New(Config{
		Memory: Arm{Enabled: true, Interval: 1},
		Skills: Arm{Enabled: true, Interval: 3},
	}, func(k ReviewKind, _ []core.Message) { fired <- k })

	r.Observe(endTurn(3)) // memory due (1 turn) AND skills due (3 iters)
	k := waitFire(t, fired)
	if !k.Has(KindMemory) || !k.Has(KindSkills) {
		t.Fatalf("want combined, got %v", k)
	}
}

func TestSkipsNonEndTurn(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	r := New(Config{Memory: Arm{Enabled: true, Interval: 1}}, func(k ReviewKind, _ []core.Message) { fired <- k })

	r.Observe(core.Result{StopReason: core.StopCancelled, ToolUses: 9})
	r.Observe(core.Result{StopReason: core.StopMaxSteps})
	assertNoFire(t, fired) // neither counted

	r.Observe(endTurn(0)) // clean turn → fires
	if k := waitFire(t, fired); !k.Has(KindMemory) {
		t.Fatalf("want memory, got %v", k)
	}
}

func TestConcurrencyCapDropsAndRetries(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	r := New(Config{Memory: Arm{Enabled: true, Interval: 1}}, func(k ReviewKind, _ []core.Message) {
		started <- struct{}{}
		<-release // block until released → keeps the review in-flight
		fired <- k
	})

	r.Observe(endTurn(0)) // fires review #1 (now in-flight, blocked)
	<-started

	r.Observe(endTurn(0)) // arrives while #1 in-flight → dropped, NOT reset
	select {
	case <-started:
		t.Fatal("a second review started while one was in flight")
	case <-time.After(80 * time.Millisecond):
	}

	close(release) // let #1 finish (and any later review won't block now)
	if k := waitFire(t, fired); !k.Has(KindMemory) {
		t.Fatalf("want memory, got %v", k)
	}

	// The dropped trigger left the counter tripped: the next clean turn fires.
	r.Observe(endTurn(0))
	<-started
	if k := waitFire(t, fired); !k.Has(KindMemory) {
		t.Fatalf("retry: want memory, got %v", k)
	}
}

// TestSkillIterCounterCappedDuringInFlight guards against the post-release
// burst: while a long review is in flight every Observe accumulates
// ToolUses indefinitely; without a cap the counter ends up far above the
// threshold and fires on every turn for many turns after the release.
// The cap (2× the threshold) limits this to at most two immediate refires.
func TestSkillIterCounterCappedDuringInFlight(t *testing.T) {
	r := New(Config{Skills: Arm{Enabled: true, Interval: 5}}, func(ReviewKind, []core.Message) {
		// don't drain — keep inFlight=true on the first trip
	})
	// First Observe trips and starts a review that never returns (the
	// callback above is a no-op, but inFlight is cleared inside r.run's
	// defer — let's instead drive accumulator-only Observes that drop).
	r.mu.Lock()
	r.inFlight = true // simulate a stuck in-flight review
	r.mu.Unlock()

	for range 50 {
		r.Observe(endTurn(10)) // 500 cumulative tool-iters across 50 turns
	}
	r.mu.Lock()
	got := r.itersSinceSkill
	r.mu.Unlock()
	if got > 2*r.skillEvery {
		t.Fatalf("itersSinceSkill = %d, expected cap at 2×%d=%d", got, r.skillEvery, 2*r.skillEvery)
	}
}

func TestSeedTurns(t *testing.T) {
	fired := make(chan ReviewKind, 4)
	r := New(Config{Memory: Arm{Enabled: true, Interval: 5}}, func(k ReviewKind, _ []core.Message) { fired <- k })

	r.SeedTurns(4) // 4 prior user turns → 1 more should trip the cadence
	r.Observe(endTurn(0))
	if k := waitFire(t, fired); !k.Has(KindMemory) {
		t.Fatalf("want memory after seed, got %v", k)
	}
}

func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty config should be disabled")
	}
	if !(Config{Skills: Arm{Enabled: true}}).Enabled() {
		t.Fatal("skills-on should be enabled")
	}
}
