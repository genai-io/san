// Package cron schedules recurring and one-shot jobs that fire user
// prompts back into the agent loop. Exposes *Scheduler directly.
package cron

// Options configures the package-level *Scheduler.
type Options struct {
	StoragePath string // file path for durable job persistence
}

// Initialize creates and configures the package-level *Scheduler.
func Initialize(opts Options) {
	s := NewScheduler()
	if opts.StoragePath != "" {
		s.SetStoragePath(opts.StoragePath)
	}
	defaultScheduler = s
}

// Default returns the package-level *Scheduler.
func Default() *Scheduler {
	return defaultScheduler
}

var defaultScheduler = NewScheduler()
