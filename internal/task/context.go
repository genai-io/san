package task

import "context"

type supervisorKey struct{}

// WithSupervisor binds the owner's task pool without changing request cancellation.
func WithSupervisor(ctx context.Context, supervisor *Supervisor) context.Context {
	return context.WithValue(ctx, supervisorKey{}, supervisor)
}

// FromContext retains the native default for hosts without an explicit pool.
func FromContext(ctx context.Context) *Supervisor {
	if supervisor, ok := ctx.Value(supervisorKey{}).(*Supervisor); ok && supervisor != nil {
		return supervisor
	}
	return Default()
}

// Run tracks cleanup beyond terminal task status, including reaping and draining.
func (m *Supervisor) Run(run func()) {
	m.work.Add(1)
	m.active.Add(1)
	go func() {
		defer m.work.Done()
		defer m.active.Add(-1)
		run()
	}()
}

// Idle includes execution cleanup. Exclude launches while checking and using it.
func (m *Supervisor) Idle() bool {
	return m.active.Load() == 0 && len(m.ListRunning()) == 0
}

// Join waits for Run cleanup. Stop admitting launches before calling it.
func (m *Supervisor) Join() { m.work.Wait() }

// Cancel cancels every task in this pool, including services and finite jobs.
// Call after launch admission stops, then Join before closing dependent resources.
func (m *Supervisor) Cancel() {
	for _, task := range m.List() {
		if task.IsRunning() {
			_ = task.Kill()
		}
	}
}
