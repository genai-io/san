package input

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// run executes /goal with the given args against a controller whose session is
// already driving toward `current` (empty = no goal), returning the notice text
// and whatever message the command emitted.
func runGoal(t *testing.T, current, args string) (string, tea.Msg) {
	t.Helper()
	c := &SlashCommandController{env: SlashCommandEnv{GetGoal: func() string { return current }}}
	notice, cmd, err := c.handleGoalCommand(context.Background(), args)
	if err != nil {
		t.Fatalf("handleGoalCommand(%q) err = %v", args, err)
	}
	if cmd == nil {
		return notice, nil
	}
	return notice, cmd()
}

func TestGoalCommandStatesTheGoal(t *testing.T) {
	notice, msg := runGoal(t, "", "  ship the release  ")
	if notice != "" {
		t.Errorf("stating a goal should speak through the app, not a notice; got %q", notice)
	}
	set, ok := msg.(GoalSetMsg)
	if !ok {
		t.Fatalf("got %T, want GoalSetMsg", msg)
	}
	if set.Goal != "ship the release" {
		t.Errorf("Goal = %q, want the trimmed goal", set.Goal)
	}
}

func TestGoalCommandClears(t *testing.T) {
	for _, arg := range []string{"clear", "CLEAR"} {
		_, msg := runGoal(t, "ship the release", arg)
		if _, ok := msg.(GoalClearedMsg); !ok {
			t.Errorf("/goal %s produced %T, want GoalClearedMsg", arg, msg)
		}
	}
}

func TestGoalCommandReportsTheCurrentGoal(t *testing.T) {
	notice, msg := runGoal(t, "ship the release", "")
	if msg != nil {
		t.Errorf("reporting a goal should change nothing; got %T", msg)
	}
	if !strings.Contains(notice, "ship the release") {
		t.Errorf("notice = %q, want the current goal", notice)
	}

	notice, msg = runGoal(t, "", "")
	if msg != nil {
		t.Errorf("bare /goal with none set should change nothing; got %T", msg)
	}
	if !strings.Contains(notice, "No goal set") || !strings.Contains(notice, "/goal clear") {
		t.Errorf("notice = %q, want the no-goal usage text", notice)
	}
}

// A goal is the instruction the rest of the run answers to, so it stays in the
// transcript rather than vanishing like a config command.
func TestGoalIsKeptInTheTranscript(t *testing.T) {
	if !shouldPreserveCommandInConversation("/goal ship the release") {
		t.Error("/goal was dropped from the transcript")
	}
}
