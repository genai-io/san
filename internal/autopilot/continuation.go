package autopilot

import (
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
)

// Continuation is a snapshot of the mission policy and its consumed budget.
type Continuation struct {
	Config  setting.AutoPilotSettings
	Engaged bool
	Used    int
}

func (c Continuation) spent() bool {
	return !c.Config.ContinuationsUnlimited() && c.Used >= c.Config.ResolvedMaxContinuations()
}

func (c Continuation) driving() bool { return c.Engaged && c.Config.Steers.TurnEnd }

type Disposition int

const (
	Ignore Disposition = iota
	Decide
	Pause
	BudgetHandback
)

func (c Continuation) TurnEnd(reason core.StopReason) (Disposition, string) {
	if c.Config.MissionState != setting.MissionRunning {
		return Ignore, ""
	}
	resumable, situation := StopEvidence(reason)
	if !c.driving() || !resumable {
		return Pause, ""
	}
	if c.spent() {
		return BudgetHandback, ""
	}
	return Decide, situation
}

// A kick opens a mission; it is not a turn-end steer.
func (c Continuation) CanKick() bool { return c.Engaged && !c.spent() }

func (c Continuation) Recovery(attempts int) Disposition {
	if !c.driving() || c.Config.MissionState != setting.MissionRunning {
		return Ignore
	}
	// Bound consecutive failures even when the continuation budget is unlimited.
	const maxRecoveries = 3
	if attempts >= maxRecoveries || c.spent() {
		return Pause
	}
	return Decide
}

func (c Continuation) StartBudget() (used int, resuming bool) {
	if c.Config.MissionState != setting.MissionPaused {
		return 0, false
	}
	if c.spent() {
		return 0, true
	}
	return c.Used, true
}
