package setting

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/log"
)

// Loader handles loading and merging settings from multiple sources.
type Loader struct {
	userDir      string
	projectDir   string
	projectRoot  string
	claudeCompat bool
}

// NewLoader creates a loader with default paths (~/.san, .san) and Claude compatibility enabled.
func NewLoader() *Loader {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Logger().Warn("failed to determine home directory, user-level settings will be unavailable", zap.Error(err))
	}
	userDir := ""
	if homeDir != "" {
		userDir = confdir.Dir(homeDir)
	}
	return &Loader{
		userDir:      userDir,
		projectDir:   confdir.Dir("."),
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
		userDir = confdir.Dir(homeDir)
	}
	return &Loader{
		userDir:      userDir,
		projectDir:   confdir.Dir(cwd),
		projectRoot:  cwd,
		claudeCompat: true,
	}
}

// Load loads and merges settings from all sources in priority order (lowest to highest):
//  1. ~/.claude/settings.json
//  2. ~/.san/settings.json
//  3. .claude/settings.json
//  4. .san/settings.json
//  5. .claude/settings.local.json
//  6. .san/settings.local.json
func (l *Loader) Load() (*Data, error) {
	homeDir, _ := os.UserHomeDir()

	// Two-phase loading: first load Claude-compat settings, then San-native.
	// For hooks, San-native settings override Claude-compat settings per event
	// to prevent incompatible hooks (e.g., Claude Code's interactive protocol)
	// from blocking San's own hooks.

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

	// Collect hooks separately: Claude-compat hooks and San-native hooks.
	// For native hooks, higher-priority sources REPLACE lower-priority sources
	// per event (project overrides user, local overrides project).
	claudeHooks := make(map[string][]Hook)
	nativeHooks := make(map[string][]Hook) // last write wins per event

	settings := NewData()
	for _, src := range sources {
		data, err := os.ReadFile(src.path)
		if err != nil {
			continue
		}
		var s Data
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
		// This means project .san/settings.json can set "PermissionRequest": []
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
	// incompatible with San's TUI-based approval flow and can cause hangs.
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
func (l *Loader) LoadFile(path string) (*Data, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Data
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveToProject saves settings to the project-level settings file, merging with existing.
func (l *Loader) SaveToProject(settings *Data) error {
	return l.saveToFile(filepath.Join(l.projectDir, "settings.json"), settings)
}

// SaveToUser saves settings to the user-level settings file, merging with existing.
func (l *Loader) SaveToUser(settings *Data) error {
	return l.saveToFile(filepath.Join(l.userDir, "settings.json"), settings)
}

func (l *Loader) saveToFile(path string, settings *Data) error {
	toSave := settings
	if data, err := os.ReadFile(path); err == nil {
		existing := NewData()
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
	loadedSettings   *Data
	loadedSettingsMu sync.Mutex
)

// Load loads settings using the default loader (cached after first call).
func Load() (*Data, error) {
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
func Reload() (*Data, error) {
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
func LoadForCwd(cwd string) (*Data, error) {
	return NewLoaderForCwd(cwd).Load()
}

// defaultData returns default settings without loading from disk.
func defaultData() *Data {
	return NewData()
}

// UpdateDisabledToolsAt updates disabled tools at user level (true) or project level (false).
func UpdateDisabledToolsAt(disabledTools map[string]bool, userLevel bool) error {
	loader := NewLoader()
	settings := &Data{DisabledTools: disabledTools}

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

// UpdateSelfLearnAt persists the L1 self-learning config at the requested
// settings level (true = user-wide, false = project-local). The new value
// is merged with whatever else lives in that settings file — only the
// selfLearn block is rewritten. Returns Validate's error verbatim if the
// new config is illegal (§3.1) so the caller can surface it inline before
// touching disk.
func UpdateSelfLearnAt(cfg SelfLearnSettings, userLevel bool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	loader := NewLoader()
	path := filepath.Join(loader.projectDir, "settings.json")
	if userLevel {
		path = filepath.Join(loader.userDir, "settings.json")
	}

	// Read the existing settings for THIS file and replace only the
	// selfLearn block. We deliberately do NOT route through
	// SaveToUser/SaveToProject (which merge via mergeSelfLearn): that merge
	// ORs the boolean fields, so reusing it here would OR the new config
	// with the file's own previous value and a true→false toggle (e.g.
	// disabling an arm from /config) could never be persisted. Replacing
	// the block lets the off-toggle actually stick while leaving every
	// other setting in the file untouched. (Cross-level layering still ORs
	// on Load by design — disabling at a lower-priority level cannot
	// override an enable at a higher-priority one.)
	existing := NewData()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, existing)
	}
	existing.SelfLearn = cfg
	if err := writeJSONAtomic(path, existing); err != nil {
		return err
	}

	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// UpdateAutoReviewAt persists the autopilot config at the requested settings
// level (true = user-wide, false = project-local). Like UpdateSelfLearnAt it
// reads the target file and replaces only the autoPilot block, so an off-toggle
// actually sticks: routing through SaveToUser/SaveToProject would re-merge via
// mergeAutoReview and OR the steer bools with the file's previous value, making
// a true→false toggle impossible to persist. Every other setting in the file is
// left untouched. (Cross-level layering still ORs on Load by design.)
func UpdateAutoReviewAt(cfg AutoReviewSettings, userLevel bool) error {
	loader := NewLoader()
	path := filepath.Join(loader.projectDir, "settings.json")
	if userLevel {
		path = filepath.Join(loader.userDir, "settings.json")
	}

	existing := NewData()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, existing)
	}
	existing.AutoReview = cfg
	if err := writeJSONAtomic(path, existing); err != nil {
		return err
	}

	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// AutoReviewPresetDir is the folder where /autopilot Export saves named configs
// and Import reads them — a shared, non-session space for reusable presets.
func AutoReviewPresetDir() string {
	return filepath.Join(NewLoader().userDir, "autopilot")
}

// ExportAutoReview writes cfg as a named preset (<presetDir>/<name>.json),
// returning the path. The name is reduced to a bare filename and must be
// non-empty.
func ExportAutoReview(name string, cfg AutoReviewSettings) (string, error) {
	base := sanitizePresetName(name)
	if base == "" {
		return "", fmt.Errorf("preset name required")
	}
	dir := AutoReviewPresetDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, base+".json")
	if err := writeJSONAtomic(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

// ListAutoReviewPresets returns the saved preset names (without extension),
// sorted. A missing directory yields an empty list, not an error.
func ListAutoReviewPresets() ([]string, error) {
	entries, err := os.ReadDir(AutoReviewPresetDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names, nil
}

// ImportAutoReview reads a named preset back into an AutoReviewSettings.
func ImportAutoReview(name string) (AutoReviewSettings, error) {
	var cfg AutoReviewSettings
	base := sanitizePresetName(name)
	if base == "" {
		return cfg, fmt.Errorf("preset name required")
	}
	data, err := os.ReadFile(filepath.Join(AutoReviewPresetDir(), base+".json"))
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	return cfg, err
}

// sanitizePresetName reduces a user-entered name to a safe bare filename so a
// preset can never escape the preset directory.
func sanitizePresetName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".json")
	name = filepath.Base(name) // strip any path components
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return ""
	}
	return name
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
// userLevel=true reads from ~/.san/settings.json; false reads from .san/settings.json.
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

// PersonaAt returns the persona pinned in a single settings file (not merged).
// userLevel=true reads ~/.san/settings.json; false reads <cwd>/.san/settings.json.
func PersonaAt(cwd string, userLevel bool) string {
	loader := NewLoader()
	if cwd != "" {
		loader = NewLoaderForCwd(cwd)
	}
	dir := loader.projectDir
	if userLevel {
		dir = loader.userDir
	}
	if dir == "" {
		return ""
	}
	s, err := loader.LoadFile(filepath.Join(dir, "settings.json"))
	if err != nil || s == nil {
		return ""
	}
	return s.Persona
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

	settings := &Data{
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

// SaveTheme persists the chosen theme to ~/.san/settings.json.
func SaveTheme(t string) error {
	if err := NewLoader().SaveToUser(&Data{Theme: t}); err != nil {
		return err
	}
	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// SaveContextBar persists the context-usage bar on/off choice to
// ~/.san/settings.json. The value is written as an explicit pointer so an
// "off" choice overrides any inherited "on" instead of being dropped as a
// zero value (see Data.ContextBar).
func SaveContextBar(on bool) error {
	if err := NewLoader().SaveToUser(&Data{ContextBar: &on}); err != nil {
		return err
	}
	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}

// SavePersonaAt persists the chosen persona name at the given scope: the
// project file (.san/settings.json under cwd) when userLevel is false, or the
// user file (~/.san/settings.json) when true. An empty name clears the field.
// It bypasses mergeSettings so an empty value can actually clear it on disk.
//
// Scope matters: a project-scoped persona should save project-level so the
// selection lives with the persona (and doesn't leak to other projects, where
// it wouldn't resolve); user-scoped personas save user-level to become the
// default everywhere.
func SavePersonaAt(cwd, name string, userLevel bool) error {
	loader := NewLoader()
	if cwd != "" {
		loader = NewLoaderForCwd(cwd)
	}
	dir := loader.projectDir
	if userLevel {
		dir = loader.userDir
	}
	if dir == "" {
		return os.ErrNotExist
	}
	path := filepath.Join(dir, "settings.json")

	existing := NewData()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, existing)
	}
	existing.Persona = name

	if err := writeJSONAtomic(path, existing); err != nil {
		return err
	}

	loadedSettingsMu.Lock()
	loadedSettings = nil
	loadedSettingsMu.Unlock()
	return nil
}
