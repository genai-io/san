package task

import (
	"errors"
	"sync"
)

// StopServices stops session-owned services with the normal graceful-stop and
// force-kill fallback. An empty sessionID selects all sessions for application
// shutdown. It must not be called merely because the active session changes.
func (m *Manager) StopServices(sessionID string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, t := range m.List() {
		info := t.GetStatus()
		if info.Lifetime != LifetimeService || !t.IsRunning() || (sessionID != "" && info.ParentSessionID != sessionID) {
			continue
		}
		wg.Add(1)
		go func(t BackgroundTask) {
			defer wg.Done()
			if err := m.Kill(t.GetID()); err != nil && t.IsRunning() {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	return errors.Join(errs...)
}
