package task

import "context"

type managerKey struct{}

// WithManager binds the owner's task pool without changing request cancellation.
func WithManager(ctx context.Context, manager *Manager) context.Context {
	return context.WithValue(ctx, managerKey{}, manager)
}

// FromContext retains the native default for hosts without an explicit pool.
func FromContext(ctx context.Context) *Manager {
	if manager, ok := ctx.Value(managerKey{}).(*Manager); ok && manager != nil {
		return manager
	}
	return Default()
}

// Run tracks cleanup beyond terminal task status, including reaping and draining.
func (m *Manager) Run(run func()) {
	m.work.Add(1)
	m.active.Add(1)
	go func() {
		defer m.work.Done()
		defer m.active.Add(-1)
		run()
	}()
}

// Idle includes execution cleanup. Exclude launches while checking and using it.
func (m *Manager) Idle() bool {
	return m.active.Load() == 0 && len(m.ListRunning()) == 0
}

// Join waits for Run cleanup. Stop admitting launches before calling it.
func (m *Manager) Join() { m.work.Wait() }

// Cancel cancels every task in this pool, including services and finite jobs.
// Call after launch admission stops, then Join before closing dependent resources.
func (m *Manager) Cancel() {
	for _, task := range m.List() {
		if task.IsRunning() {
			_ = task.Kill()
		}
	}
}
