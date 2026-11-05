package task

import "context"

type managerKey struct{}

// WithManager binds tool execution to its owner's task pool. It preserves the
// request's cancellation; background work is joined separately by the owner.
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

// Run tracks execution cleanup, not the task's display status. Kill may mark a
// task terminal before its process has been reaped or its output drained.
// The owner must stop admitting launches before calling Join.
func (m *Manager) Run(run func()) {
	m.work.Add(1)
	go func() {
		defer m.work.Done()
		run()
	}()
}

// Join waits for all execution cleanup registered with Run.
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
