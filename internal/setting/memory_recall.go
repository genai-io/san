package setting

import (
	"fmt"
	"path/filepath"
	"time"
)

// MemoryRecallMode controls whether and when a turn looks up Auto Memory.
type MemoryRecallMode string

const (
	MemoryRecallFast MemoryRecallMode = "fast" // Start the turn without waiting.
	MemoryRecallWait MemoryRecallMode = "wait" // Wait for lookup up to the timeout.
	MemoryRecallOff  MemoryRecallMode = "off"  // Do not look up memories.
)

// MemoryRecallConfig is the resolved lookup policy, independent of enablement.
type MemoryRecallConfig struct {
	Mode    MemoryRecallMode
	Timeout time.Duration // Used only in Wait mode.
}

// MemoryRecall resolves unset or invalid values to Fast and 5 seconds.
func (d *Data) MemoryRecall() MemoryRecallConfig {
	cfg := MemoryRecallConfig{Mode: MemoryRecallFast, Timeout: 5 * time.Second}
	if d != nil {
		switch d.MemoryRecallLookup {
		case MemoryRecallWait, MemoryRecallOff:
			cfg.Mode = d.MemoryRecallLookup
		}
		if timeout, err := time.ParseDuration(d.MemoryRecallTimeout); err == nil && timeout > 0 {
			cfg.Timeout = timeout
		}
	}
	return cfg
}

// MemoryRecall returns the live, merged lookup policy.
func (s *Settings) MemoryRecall() MemoryRecallConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.MemoryRecall()
}

// MemoryRecallAt reads the raw mode and timeout from a single settings file.
// An empty value inherits from lower-precedence settings.
func MemoryRecallAt(cwd string, scope Scope) (MemoryRecallMode, string) {
	d := memorySettingsAt(cwd, scope)
	return d.MemoryRecallLookup, d.MemoryRecallTimeout
}

func memorySettingsAt(cwd string, scope Scope) *Data {
	l := NewLoaderForCwd(cwd)
	dir := l.dir(scope)
	if dir == "" {
		return &Data{}
	}
	d, err := l.LoadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return &Data{}
	}
	return d
}

// SaveMemoryRecallMode sets the mode at scope; empty clears the override.
func SaveMemoryRecallMode(cwd string, scope Scope, mode MemoryRecallMode) error {
	if mode != "" && mode != MemoryRecallFast && mode != MemoryRecallWait && mode != MemoryRecallOff {
		return fmt.Errorf("invalid recall mode: %q", mode)
	}
	return updateMemorySettings(cwd, scope, func(d *Data) { d.MemoryRecallLookup = mode })
}

// SaveMemoryRecallTimeout sets a positive duration (e.g. "5s") at scope;
// empty clears the override.
func SaveMemoryRecallTimeout(cwd string, scope Scope, timeout string) error {
	if timeout != "" {
		if duration, err := time.ParseDuration(timeout); err != nil || duration <= 0 {
			return fmt.Errorf("enter a positive duration (e.g. 5s)")
		}
	}
	return updateMemorySettings(cwd, scope, func(d *Data) { d.MemoryRecallTimeout = timeout })
}

func updateMemorySettings(cwd string, scope Scope, mutate func(*Data)) error {
	if scope != ScopeUser && scope != ScopeProject {
		return fmt.Errorf("invalid settings scope: %q", scope)
	}
	dir := NewLoaderForCwd(cwd).dir(scope)
	if dir == "" {
		return fmt.Errorf("settings directory unavailable")
	}
	return mutateSettingsPath(filepath.Join(dir, "settings.json"), mutate)
}
