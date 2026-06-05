// Package confdir resolves the per-user and per-project configuration directory.
//
// The directory was renamed from ".gen" to ".san". To avoid breaking existing
// installs, Dir keeps using a legacy ".gen" directory when one is already
// present and no ".san" exists yet. The whole directory moves as a unit: a
// legacy install keeps reading and writing ".gen" until the user migrates, so a
// single root never ends up split across both names.
//
// confdir is a zero-dependency infrastructure leaf so every layer (including
// internal/log and internal/core) can import it without a layering violation.
package confdir

import (
	"os"
	"path/filepath"
)

const (
	// Name is the current configuration directory name.
	Name = ".san"
	// LegacyName is the pre-rename directory name, still honored for back-compat.
	LegacyName = ".gen"
)

// Dir returns the configuration directory under root. It prefers root/.san; if
// that directory does not exist but the legacy root/.gen does, it returns the
// legacy path so existing data keeps working. When neither exists it returns
// root/.san, so fresh installs create the new name.
func Dir(root string) string {
	if current := filepath.Join(root, Name); isDir(current) {
		return current
	}
	if legacy := filepath.Join(root, LegacyName); isDir(legacy) {
		return legacy
	}
	return filepath.Join(root, Name)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
