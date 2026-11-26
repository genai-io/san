package autopilot

import (
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
)

func TestContinuationGates(t *testing.T) {
	const running = setting.MissionRunning
	for _, tc := range []struct {
		name                  string
		state                 setting.MissionState
		engaged, turnEnd      bool
		limit, used, attempts int
		stop                  core.StopReason
		turn, recovery        Disposition
		kick                  bool
	}{
		{"last available", running, true, true, 2, 1, 2, core.StopMaxSteps, Decide, Decide, true},
		{"exact cap", running, true, true, 2, 2, 0, core.StopEndTurn, BudgetHandback, Pause, false},
		{"over default cap", running, true, true, 0, setting.AutoPilotDefaultMaxContinuations + 1, 0, core.StopEndTurn, BudgetHandback, Pause, false},
		{"unlimited but out of retries", running, true, true, -1, 100, 3, core.StopEndTurn, Decide, Pause, true},
		{"unknown stop before budget", running, true, true, 1, 1, 0, "unknown", Pause, Pause, false},
		{"steer disabled before budget", running, true, false, 1, 1, 0, core.StopEndTurn, Pause, Ignore, false},
		{"kick ignores steer and paused state", setting.MissionPaused, true, false, 2, 1, 0, core.StopEndTurn, Ignore, Ignore, true},
		{"mode disabled", running, false, true, 1, 0, 0, core.StopEndTurn, Pause, Ignore, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Continuation{Config: setting.AutoPilotSettings{MissionState: tc.state, MaxContinuations: tc.limit}, Engaged: tc.engaged, Used: tc.used}
			c.Config.Steers.TurnEnd = tc.turnEnd
			got, situation := c.TurnEnd(tc.stop)
			if got != tc.turn || (situation != "") != (tc.stop == core.StopMaxSteps) {
				t.Errorf("TurnEnd = (%v,%q), want %v", got, situation, tc.turn)
			}
			if got := c.Recovery(tc.attempts); got != tc.recovery {
				t.Errorf("Recovery = %v, want %v", got, tc.recovery)
			}
			if got := c.CanKick(); got != tc.kick {
				t.Errorf("CanKick = %v, want %v", got, tc.kick)
			}
		})
	}
}

func TestStartBudget(t *testing.T) {
	for _, tc := range []struct {
		name              string
		state             setting.MissionState
		limit, used, want int
		resume            bool
	}{
		{"new", "", 2, 1, 0, false}, {"paused", setting.MissionPaused, 2, 1, 1, true}, {"exact", setting.MissionPaused, 2, 2, 0, true}, {"unlimited", setting.MissionPaused, -1, 100, 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Continuation{Config: setting.AutoPilotSettings{MissionState: tc.state, MaxContinuations: tc.limit}, Used: tc.used}
			if used, resume := c.StartBudget(); used != tc.want || resume != tc.resume {
				t.Fatalf("StartBudget = (%d,%t), want (%d,%t)", used, resume, tc.want, tc.resume)
			}
		})
	}
}
