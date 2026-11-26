package tui

import (
	"errors"
)

// closeSessionRecording runs after runtime and task shutdown, while hooks can
// still commit their outcomes to the original writer.
func (m *model) closeSessionRecording() error {
	m.retireFlowMain()
	m.cancelMemoryUpdate()
	if m.services.Hook != nil {
		m.services.Hook.Shutdown()
	}
	var err error
	if m.services.Session != nil {
		err = m.services.Session.Close()
	}
	if m.services.SessionStore != nil {
		err = errors.Join(err, m.services.SessionStore.Close())
	}
	return err
}
