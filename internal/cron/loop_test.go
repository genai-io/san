package cron

import (
	"strings"
	"testing"
	"time"
)

func TestParseLoopCommand_RoundsNonCleanIntervals(t *testing.T) {
	now := time.Date(2026, 4, 6, 14, 30, 0, 0, time.Local)

	parsed, err := ParseLoopCommand("90m check the deploy", now)
	if err != nil {
		t.Fatalf("ParseLoopCommand failed: %v", err)
	}
	if parsed.Cron != "37 */2 * * *" {
		t.Fatalf("unexpected cron expression %q", parsed.Cron)
	}
	if !strings.Contains(parsed.Note, "Rounded `90m` to `every 2 hour(s)`") {
		t.Fatalf("expected rounding note, got %q", parsed.Note)
	}
}

func TestParseLoopCommand_AvoidsTopOfHourScheduling(t *testing.T) {
	now := time.Date(2026, 4, 6, 10, 0, 0, 0, time.Local)

	parsed, err := ParseLoopCommand("1h check deploy", now)
	if err != nil {
		t.Fatalf("ParseLoopCommand failed: %v", err)
	}
	if parsed.Cron != "7 * * * *" {
		t.Fatalf("unexpected cron expression %q", parsed.Cron)
	}
}

func TestLoopOnceKeepsAbsoluteDeadline(t *testing.T) {
	now := time.Date(2025, 1, 31, 23, 59, 59, 123, time.UTC)
	cmd, err := ParseLoopOnceCommand("1m remind me", now)
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler()
	job, err := s.CreateLoop(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(time.Minute); !job.NextFire.Equal(want) {
		t.Fatalf("deadline = %v, want %v", job.NextFire, want)
	}
	if fired := s.Tick(); len(fired) != 1 || !s.Empty() {
		t.Fatalf("overdue one-shot not delivered exactly once: %v", fired)
	}
}

func TestMultiDayLoopKeepsAnchorAcrossMonthEnd(t *testing.T) {
	now := time.Date(2026, 1, 29, 10, 15, 0, 0, time.UTC)
	cmd, err := ParseLoopCommand("check deploy every 2 days", now)
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler()
	job, err := s.CreateLoop(cmd)
	if err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{2, 4, 6} {
		base := now.AddDate(0, 0, days)
		if !job.loopBase.Equal(base) || job.NextFire.Before(base) || !job.NextFire.Before(base.Add(maxRecurringJitter)) {
			t.Fatalf("base = %v, fire = %v; want base %v + bounded jitter", job.loopBase, job.NextFire, base)
		}
		s.fire(job, job.NextFire.Add(time.Hour), nil, nil)
	}
}

func TestParseLoopOnceCommand_SupportsTrailingInClause(t *testing.T) {
	now := time.Date(2026, 4, 6, 10, 5, 0, 0, time.Local)

	parsed, err := ParseLoopOnceCommand("check deploy in 20m", now)
	if err != nil {
		t.Fatalf("ParseLoopOnceCommand failed: %v", err)
	}
	if parsed.Prompt != "check deploy" {
		t.Fatalf("unexpected prompt %q", parsed.Prompt)
	}
	if !parsed.deadline.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("unexpected deadline %v", parsed.deadline)
	}
	if !strings.Contains(parsed.Human, "once at 2026-04-06 10:25") {
		t.Fatalf("unexpected human schedule %q", parsed.Human)
	}
}
