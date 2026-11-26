package task

import (
	"github.com/genai-io/san/internal/infra/log"
)

// Shutdown stops background services before canceling and joining their tasks.
func (tasks *Manager) Shutdown() error {
	if tasks == nil {
		return nil
	}
	err := tasks.StopServices("")
	if err != nil {
		log.Logger().Sugar().Warnf("stop background services: %v", err)
	}
	tasks.Cancel()
	tasks.Join()
	return err
}
