package task

import "fmt"

// KillAttempt stops exactly the agent execution selected by a caller. Graceful
// waiting and force-kill escalation remain pinned to that generation even if
// another execution finishes or starts while the caller is waiting.
func (m *Manager) KillAttempt(id string, attempt int) error {
	worker, exists := m.Get(id)
	if !exists {
		return fmt.Errorf("task not found: %s", id)
	}
	agent, ok := worker.(*AgentTask)
	if !ok {
		return fmt.Errorf("task %s does not support agent attempts", id)
	}
	if err := agent.StopAttempt(attempt); err != nil {
		return err
	}
	if agent.WaitForAttemptCompletion(attempt, gracefulStopTimeout) {
		return nil
	}
	return agent.KillAttempt(attempt)
}
