package setting

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"go.uber.org/zap"

	"github.com/genai-io/gen-code/internal/log"
)

// Loader handles loading and merging settings from multiple sources.
type Loader struct {
	userDir      string
	projectDir   string
	projectRoot  string
	claudeCompat bool
}

// NewLoader creates a loader with default paths (~/.gen, .gen) and Claude compatibility enabled.
func NewLoader() *Loader {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Logger().Warn("failed to determine home directory, user-level settings will be unavailable", zap.Error(err))
	}
	userDir := ""
	if homeDir != "" {
		userDir = filepath.Join(homeDir, ".gen")
	}
	return &Loader{
		userDir:      userDir,
		projectDir:   ".gen",
		projectRoot:  ".",
		claudeCompat: true,
	}
}

// NewLoaderWithOptions creates a loader with custom options.
func NewLoaderWithOptions(userDir, projectDir string, claudeCompat bool) *Loader {
	return &Loader{
		userDir:      userDir,
		projectDir:   projectDir,
		projectRoot:  filepath.Dir(projectDir),
		claudeCompat: claudeCompat,
	}
}

// NewLoaderForCwd creates a loader rooted at the provided working directory.
func NewLoaderForCwd(cwd string) *Loader {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Logger().Warn("failed to determine home directory, user-level settings will be unavailable", zap.Error(err))
	}
	userDir := ""
	if homeDir != "" {
		userDir = filepath.Join(homeDir, ".gen")
	}
	return &Loader{
		userDir:      userDir,
		projectDir:   filepath.Join(cwd, ".gen"),
		projectRoot:  cwd,
		claudeCompat: true,
	}
}

// Load loads and merges settings from all sources in priority order (lowest to highest):
//  1. ~/.claude/settings.json
//  2. ~/.gen/settings.json
//  3. .claude/settings.json
//  4. .gen/settings.json
//  5. .claude/settings.local.json
//  6. .gen/settings.local.json
func (l *Loader) Load() (*Settings, error) {
	homeDir, _ := os.UserHomeDir()

	// Two-phase loading: first load Claude-compat settings, then GenCode-native.
	// For hooks, GenCode-native settings override Claude-compat settings per event
	// to prevent incompatible hooks (e.g., Claude Code's interactive protocol)
	// from blocking GenCode's own hooks.

	type source struct {
		path         string
		claudeCompat bool
	}

	var sources []source
	if l.claudeCompat && homeDir != "" {
		sources = append(sources, source{filepath.Join(homeDir, ".claude", "settings.json"), true})
	}
	if l.userDir != "" {
		sources = append(sources, source{filepath.Join(l.userDir, "settings.json"), false})
	}
	if l.claudeCompat {
		sources = append(sources, source{filepath.Join(l.projectRoot, ".claude", "settings.json"), true})
	}
	sources = append(sources, source{filepath.Join(l.projectDir, "settings.json"), false})
	if l.claudeCompat {
		sources = append(sources, source{filepath.Join(l.projectRoot, ".claude", "settings.local.json"), true})
	}
	sources = append(sources, source{filepath.Join(l.projectDir, "settings.local.json"), false})

	// Collect hooks separately: Claude-compat hooks and GenCode-native hooks.
	// For native hooks, higher-priority sources REPLACE lower-priority sources
	// per event (project overrides user, local overrides project).
	claudeHooks := make(map[string][]Hook)
	nativeHooks := make(map[string][]Hook) // last write wins per event

	settings := NewSettings()
	for _, src := range sources {
		data, err := os.ReadFile(src.path)
		if err != nil {
			continue
		}
		var s Settings
		if err := json.Unmarshal(data, &s); err != nil {
			log.Logger().Warn("failed to parse config file", zap.String("path", src.path), zap.Error(err))
			continue
		}

		// Extract hooks before merging — we'll merge hooks manually
		srcHooks := s.Hooks
		s.Hooks = nil
		settings = mergeSettings(settings, &s)

		// Accumulate hooks by source type.
		// Native hooks: higher-priority sources replace lower-priority per event.
		// This means project .gen/settings.json can set "PermissionRequest": []
		// to disable user-level PermissionRequest hooks.
		for event, hooks := range srcHooks {
			if src.claudeCompat {
				claudeHooks[event] = append(claudeHooks[event], hooks...)
			} else {
				nativeHooks[event] = hooks
			}
		}
	}

	// Merge hooks: for each event, use native hooks if available, otherwise use Claude-compat hooks.
	// PermissionRequest hooks are NEVER inherited from Claude-compat sources because
	// Claude Code's interactive permission protocol (e.g., vibe-island-bridge) is
	// incompatible with GenCode's TUI-based approval flow and can cause hangs.
	merged := make(map[string][]Hook)
	for event, hooks := range claudeHooks {
		if event == "PermissionRequest" {
			continue // skip — incompatible protocol
		}
		if _, hasNative := nativeHooks[event]; !hasNative {
			merged[event] = hooks
		}
	}
	for event, hooks := range nativeHooks {
		if len(hooks) > 0 {
			merged[event] = hooks
		}
		// Empty hooks array means explicitly disabled — don't add to merged
	}
	settings.Hooks = merged

	return settings, nil
}

// LoadFile loads settings from a specific file.
func (l *Loader) LoadFile(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveToProject saves settings to the project-level settings file, merging with existing.
func (l *Loader) SaveToProject(settings *Settings) error {
	return l.saveToFile(filepath.Join(l.projectDir, "settings.json"), settings)
}

// SaveToUser saves settings to the user-level settings file, merging with existing.
func (l *Loader) SaveToUser(settings *Settings) error {
	return l.saveToFile(filepath.Join(l.userDir, "settings.json"), settings)
}

func (l *Loader) saveToFile(path string, settings *Settings) error {
	toSave := settings
	if data, err := os.ReadFile(path); err == nil {
		existing := NewSettings()
		if err := json.Unmarshal(data, existing); err == nil {
			toSave = mergeSettings(existing, settings)
		}
	}
	return writeJSONAtomic(path, toSave)
}

// writeJSONAtomic marshals v as indented JSON and writes it to path via a
// temp file + rename so a kill mid-write cannot truncate the target.
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

var (
	loadedSettings   *Settings
	loadedSettingsMu sync.Mutex
)

// Load loads settings using the default loader (cached after first call).
func Load() (*Settings, error) {
	loadedSettingsMu.Lock()
	defer loadedSettingsMu.Unlock()
	if loadedSettings != nil {
		return loadedSettings, nil
	}
	s, err := NewLoader().Load()
	if err != nil {
		return nil, err
	}
	loadedSettings = s
	return s, nil
}

// Reload clears the settings cache and reloads from disk.
func Reload() (*Settings, error) {
	loadedSettingsMu.Lock()
	defer loadedSettingsMu.Unlock()
	s, err := NewLoader().Load()
	if err != nil {
		return nil, err
	}
	loadedSettings = s
	return s, nil
}

// LoadForCwd loads settings for the provided working directory without using
// the process-global cache. This is used when the session cwd changes.
func LoadForCwd(cwd string) (*Settings, error) {
	return NewLoaderForCwd(cwd).Load()
}

// defaultSettings returns default settings without loading from disk.
func defaultSettings() *Settings {
	return NewSettings()
}

// UpdateDisabledToolsAt updates disabled tools at user level (true) or project level (false).
func UpdateDisabledToolsAt(disabledTools map[string]bool, userLevel bool) error {
	loader := NewLoader()
	settings := &Settings{DisabledTools: disabledTools}

	var err error
	if userLevel {
		err = loader.SaveToUser(settings)
	} else {
		err = loader.SaveToProject(settings)
	}
	if err != nil {
		return err
	}

	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// GetDisabledTools returns the merged disabled tools map from loaded settings.
// Returns a copy so callers cannot mutate the cached settings.
func GetDisabledTools() map[string]bool {
	s, err := Load()
	if err != nil || s.DisabledTools == nil {
		return make(map[string]bool)
	}
	result := make(map[string]bool, len(s.DisabledTools))
	maps.Copy(result, s.DisabledTools)
	return result
}

// GetDisabledToolsAt returns disabled tools from a single settings file (not merged).
// userLevel=true reads from ~/.gen/settings.json; false reads from .gen/settings.json.
func GetDisabledToolsAt(userLevel bool) map[string]bool {
	loader := NewLoader()
	path := filepath.Join(loader.projectDir, "settings.json")
	if userLevel {
		path = filepath.Join(loader.userDir, "settings.json")
	}
	s, err := loader.LoadFile(path)
	if err != nil || s.DisabledTools == nil {
		return make(map[string]bool)
	}
	result := make(map[string]bool, len(s.DisabledTools))
	maps.Copy(result, s.DisabledTools)
	return result
}

// AddAllowRuleAt appends a permission allow rule to project settings rooted at
// the provided cwd.
func AddAllowRuleAt(toolName string, args map[string]any, cwd string) error {
	return AddAllowRuleDirectlyAt(BuildRule(toolName, args), cwd)
}

// AddAllowRuleDirectlyAt appends a pre-built allow rule string to the project
// settings associated with cwd. When cwd is empty, it uses the process cwd.
func AddAllowRuleDirectlyAt(rule, cwd string) error {
	if rule == "" {
		return nil
	}

	loader := NewLoader()
	if cwd != "" {
		loader = NewLoaderForCwd(cwd)
	}
	path := filepath.Join(loader.projectDir, "settings.json")

	// Load existing to check for duplicates
	existing, _ := loader.LoadFile(path)
	if existing != nil && slices.Contains(existing.Permissions.Allow, rule) {
		return nil // already exists
	}

	settings := &Settings{
		Permissions: PermissionSettings{
			Allow: []string{rule},
		},
	}
	if err := loader.SaveToProject(settings); err != nil {
		return err
	}
	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// LoadTheme returns the configured theme string, or "" if none is set.
func LoadTheme() string {
	s, err := Load()
	if err != nil || s == nil {
		return ""
	}
	return s.Theme
}

// SaveTheme persists the chosen theme to ~/.gen/settings.json.
func SaveTheme(t string) error {
	if err := NewLoader().SaveToUser(&Settings{Theme: t}); err != nil {
		return err
	}
	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// SaveIdentity persists the chosen identity name to ~/.gen/settings.json.
// An empty name clears the override so the built-in default is used.
//
// Bypasses mergeSettings (which preserves existing string fields when the
// overlay is empty) so we can actually clear the value on disk.
func SaveIdentity(name string) error {
	loader := NewLoader()
	if loader.userDir == "" {
		return os.ErrNotExist
	}
	path := filepath.Join(loader.userDir, "settings.json")

	existing := NewSettings()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, existing)
	}
	existing.Identity = name

	if err := writeJSONAtomic(path, existing); err != nil {
		return err
	}

	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}
