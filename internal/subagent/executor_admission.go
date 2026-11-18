package subagent

import (
	"context"
	"errors"

	"github.com/genai-io/san/internal/task"
)

// admitContinuation publishes the next attempt or releases its setup resources.
func (e *Executor) admitContinuation(ctx context.Context, cancel context.CancelFunc, worker *task.AgentTask, previous int, run *preparedRun) error {
	_, err := worker.BeginAttemptWithSetup(ctx, cancel, previous, e.parentSessionID, task.AttemptSetup{
		Prepare: func() error {
			if err := e.openSession(ctx, run); err != nil {
				return err
			}
			if e.messaging {
				run.inbox = newHeldRunInbox(ctx, run.req.TaskID, run.recording)
			}
			return nil
		},
		Accept: func() error {
			if err := e.acceptContinuationInput(ctx, run); err != nil {
				return err
			}
			if run.inbox != nil {
				run.inbox.open()
			}
			return nil
		},
	})
	if err != nil {
		if run.inbox != nil {
			run.inbox.close()
		}
		if run.recording != nil {
			if storageErr := run.recording.Err(); storageErr != nil {
				run.checkpoint.persistenceErr = storageErr
			}
			err = errors.Join(err, run.recording.Close())
		}
		cancel()
		return err
	}
	if run.inbox != nil {
		// Wake may synchronously deliver mail; the task lock must be released.
		run.inbox.signalCapacity()
	}
	return nil
}
