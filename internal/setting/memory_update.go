package setting

import (
	"fmt"
	"os"
	"path/filepath"
)

// MemoryUpdate reports whether automatic memory updates are opted in,
// independently of the Auto Memory master switch.
func (s *Settings) MemoryUpdate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data != nil && s.data.MemoryUpdate != nil && *s.data.MemoryUpdate
}

// SaveMemoryUpdate writes the automatic memory update toggle at scope.
func SaveMemoryUpdate(cwd string, scope Scope, enabled bool) error {
	if scope != ScopeUser && scope != ScopeProject {
		return fmt.Errorf("invalid settings scope: %q", scope)
	}
	dir := NewLoaderForCwd(cwd).dir(scope)
	if dir == "" {
		return os.ErrNotExist
	}
	return mutateSettingsPath(filepath.Join(dir, "settings.json"), func(d *Data) {
		d.MemoryUpdate = &enabled
	})
}
