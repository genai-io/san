package tui

import (
	"errors"
)

// closeSessionRecording runs after runtime and task shutdown, while hooks can
// still commit their outcomes to the original writer.
func (m *model) closeSessionRecording() error {
	m.retireFlowMain()
	m.cancelMemoryUpdate()
	if m.features.Hook != nil {
		m.features.Hook.Shutdown()
	}
	var err error
	if m.features.Session != nil {
		err = m.features.Session.Close()
	}
	if m.features.SessionStore != nil {
		err = errors.Join(err, m.features.SessionStore.Close())
	}
	return err
}
