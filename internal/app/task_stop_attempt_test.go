package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/task"
)

func TestViewedStopCannotCancelContinuedAttempt(t *testing.T) {
	for _, boundary := range []string{"before dispatch", "before execution"} {
		t.Run(boundary, func(t *testing.T) {
			m := interactionModel()
			m.services.sessionID = "parent"
			ctx, cancel := context.WithCancel(task.WithSessionID(context.Background(), "parent"))
			defer cancel()
			worker := m.services.Task.CreateAgentTask("stable", "Reviewer", "Review", ctx, cancel)
			m.openTasks()
			msg := input.TaskStopMsg{TaskID: "stable", ViewID: m.userInput.Tasks.ViewID(), Attempt: 1}
			var cmd tea.Cmd
			if boundary == "before execution" {
				cmd = m.stopViewedTask(msg)
				if cmd == nil {
					t.Fatal("current attempt was not accepted for asynchronous stop")
				}
			}
			worker.CompleteAttempt(1, nil)
			nextCtx, nextCancel := context.WithCancel(task.WithSessionID(context.Background(), "parent"))
			defer nextCancel()
			nextAttempt, err := worker.BeginAttempt("parent", nextCtx, nextCancel)
			if err != nil || nextAttempt != 2 {
				t.Fatalf("could not create successor attempt: attempt=%d err=%v", nextAttempt, err)
			}
			defer worker.CompleteAttempt(nextAttempt, context.Canceled)
			if boundary == "before dispatch" {
				if m.stopViewedTask(msg) != nil {
					t.Fatal("stale stop action was accepted")
				}
			} else {
				m.dispatch(cmd())
			}
			if nextCtx.Err() != nil || !worker.IsRunning() || worker.GetStatus().Attempt != 2 {
				t.Fatal("stop for attempt 1 canceled the successor attempt")
			}
			if !m.userInput.Tasks.IsActive() {
				t.Fatal("rejected stale stop did not return to task inspection")
			}
		})
	}
}
