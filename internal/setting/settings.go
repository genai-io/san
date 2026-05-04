// Package config provides multi-level settings management for GenCode.
// Settings are loaded from multiple sources with the following priority (lowest to highest):
//  1. ~/.claude/settings.json (Claude user level - compatibility)
//  2. ~/.gen/settings.json (Gen user level)
//  3. .claude/settings.json (Claude project level - compatibility)
//  4. .gen/settings.json (Gen project level)
//  5. .claude/settings.local.json (Claude local level - compatibility)
//  6. .gen/settings.local.json (Gen local level)
//  7. Environment variables / CLI arguments
//  8. managed-settings.json (system level - cannot be overridden)
package setting

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// Settings represents the complete GenCode configuration.
type Settings struct {
	Permissions    PermissionSettings `json:"permissions,omitempty"`
	Model          string             `json:"model,omitempty"`
	Hooks          map[string][]Hook  `json:"hooks,omitempty"`
	Env            map[string]string  `json:"env,omitempty"`
	EnabledPlugins map[string]bool    `json:"enabledPlugins,omitempty"`
	DisabledTools  map[string]bool    `json:"disabledTools,omitempty"`
	Theme          string             `json:"theme,omitempty"`
	SearchProvider string             `json:"searchProvider,omitempty"`
	AllowBypass    *bool              `json:"allowBypass,omitempty"`
	// Identity selects an active persona under ~/.gen/identities/<name>.md or
	// .gen/identities/<name>.md. Empty = use built-in default identity.
	Identity string `json:"identity,omitempty"`
}

// PermissionSettings defines permission rules for tool execution.
// Rule format: "Tool(pattern)" — e.g. "Bash(npm:*)", "Read(**/.env)".
type PermissionSettings struct {
	DefaultMode string   `json:"defaultMode,omitempty"`
	Allow       []string `json:"allow,omitempty"`
	Deny        []string `json:"deny,omitempty"`
	Ask         []string `json:"ask,omitempty"`
}

// Hook defines an event hook configuration.
type Hook struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []HookCmd `json:"hooks,omitempty"`
}

type HookCmd struct {
	Type           string            `json:"type"`
	Command        string            `json:"command,omitempty"`
	Prompt         string            `json:"prompt,omitempty"`
	URL            string            `json:"url,omitempty"`
	If             string            `json:"if,omitempty"`
	Shell          string            `json:"shell,omitempty"`
	Model          string            `json:"model,omitempty"`
	Async          bool              `json:"async,omitempty"`
	AsyncRewake    bool              `json:"asyncRewake,omitempty"`
	Timeout        int               `json:"timeout,omitempty"`
	StatusMessage  string            `json:"statusMessage,omitempty"`
	Once           bool              `json:"once,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	AllowedEnvVars []string          `json:"allowedEnvVars,omitempty"`
}

// SessionPermissions tracks runtime permission state for the current session.
type SessionPermissions struct {
	Mode            OperationMode // Active permission mode (Normal, BypassPermissions, DontAsk, etc.)
	AllowAllEdits   bool
	AllowAllWrites  bool
	AllowAllBash    bool
	AllowAllSkills  bool
	AllowAllTasks   bool
	AllowedTools    map[string]bool
	AllowedPatterns map[string]bool
	Denials         DenialTracking // Tracks denial frequency for fallback

	// WorkingDirectories restricts Edit/Write operations to these directories.
	// When non-empty, file edits outside these dirs always prompt (bypass-immune).
	// Set automatically when entering AutoAccept mode.
	WorkingDirectories []string

	// IsBypassAvailable controls whether BypassPermissions mode can be entered.
	IsBypassAvailable bool

	// ShouldAvoidPrompts is set for headless/async subagents that cannot
	// show interactive dialogs. When true, ask → deny automatically.
	ShouldAvoidPrompts bool
}

func NewSessionPermissions() *SessionPermissions {
	return &SessionPermissions{
		AllowedTools:    make(map[string]bool),
		AllowedPatterns: make(map[string]bool),
	}
}

func (sp *SessionPermissions) AllowTool(toolName string) {
	if sp.AllowedTools == nil {
		sp.AllowedTools = make(map[string]bool)
	}
	sp.AllowedTools[toolName] = true
}

func (sp *SessionPermissions) AllowPattern(pattern string) {
	if sp.AllowedPatterns == nil {
		sp.AllowedPatterns = make(map[string]bool)
	}
	sp.AllowedPatterns[pattern] = true
}

func (sp *SessionPermissions) IsToolAllowed(toolName string) bool {
	if sp.AllowedTools[toolName] {
		return true
	}
	switch toolName {
	case "Edit":
		return sp.AllowAllEdits
	case "Write":
		return sp.AllowAllWrites
	case "Bash":
		return sp.AllowAllBash
	case "Skill":
		return sp.AllowAllSkills
	case "Agent":
		return sp.AllowAllTasks
	}
	return false
}

// AddWorkingDirectory adds a directory to the allowed working directories list.
func (sp *SessionPermissions) AddWorkingDirectory(dir string) {
	// Avoid duplicates
	for _, d := range sp.WorkingDirectories {
		if d == dir {
			return
		}
	}
	sp.WorkingDirectories = append(sp.WorkingDirectories, dir)
}

// OperationMode defines the current operation mode.
type OperationMode int

const (
	ModeNormal            OperationMode = iota
	ModeAutoAccept                      // auto-approve edits/writes
	ModeBypassPermissions               // allow all (bypass-immune checks still apply)
	ModeDontAsk                         // convert ask → deny (never prompt)
)

// allModes lists the modes that the user can cycle through with the mode toggle.
// BypassPermissions and DontAsk are entered explicitly, not via cycling.
var cycleModes = []OperationMode{ModeNormal, ModeAutoAccept}
var cycleModesWithBypass = []OperationMode{ModeNormal, ModeAutoAccept, ModeBypassPermissions}

func (m OperationMode) String() string {
	switch m {
	case ModeAutoAccept:
		return "accept edits"
	case ModeBypassPermissions:
		return "bypass permissions"
	case ModeDontAsk:
		return "don't ask"
	default:
		return "normal"
	}
}

func OperationModeFromString(mode string) OperationMode {
	mode = strings.TrimSpace(mode)
	switch mode {
	case "acceptEdits", "accept-edits", "autoAccept", "auto-accept":
		return ModeAutoAccept
	case "bypassPermissions", "bypass-permissions", "bypass":
		return ModeBypassPermissions
	case "dontAsk", "dont-ask":
		return ModeDontAsk
	default:
		return ModeNormal
	}
}

func (m OperationMode) Next() OperationMode {
	for i, mode := range cycleModes {
		if mode == m {
			return cycleModes[(i+1)%len(cycleModes)]
		}
	}
	// If current mode is not in the cycle list (e.g. BypassPermissions),
	// return to normal.
	return ModeNormal
}

// NextWithBypass cycles to the next operation mode.
// When enabled is true, BypassPermissions is included in the cycle.
func (m OperationMode) NextWithBypass(enabled bool) OperationMode {
	modes := cycleModes
	if enabled {
		modes = cycleModesWithBypass
	}
	for i, mode := range modes {
		if mode == m {
			return modes[(i+1)%len(modes)]
		}
	}
	return ModeNormal
}

func NewSettings() *Settings {
	return &Settings{
		Hooks:          make(map[string][]Hook),
		Env:            make(map[string]string),
		EnabledPlugins: make(map[string]bool),
		DisabledTools:  make(map[string]bool),
	}
}

// InitForApp loads settings for cwd, deep-clones them, and returns
// an isolated copy safe for mutation by the app layer.
// It also merges external provider preferences (e.g., search provider
// from providers.json) into the unified Settings struct.
func InitForApp(cwd string) *Settings {
	var (
		settings *Settings
		err      error
	)
	if cwd != "" {
		settings, err = LoadForCwd(cwd)
	} else {
		settings, err = Load()
	}
	_ = err
	if settings == nil {
		settings = defaultSettings()
	}
	mergeProviderPreferences(settings)
	return settings.Clone()
}

// mergeProviderPreferences reads external provider config files and merges
// relevant preferences into Settings. Currently reads searchProvider from
// ~/.gen/providers.json (owned by the llm package) so that search config
// is accessible via the unified Settings struct.
func mergeProviderPreferences(s *Settings) {
	if s.SearchProvider != "" {
		return
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(homeDir, ".gen", "providers.json"))
	if err != nil {
		return
	}
	var raw struct {
		SearchProvider *string `json:"searchProvider"`
	}
	if json.Unmarshal(data, &raw) == nil && raw.SearchProvider != nil {
		s.SearchProvider = *raw.SearchProvider
	}
}

// Clone returns a deep copy of the Settings.
func (s *Settings) Clone() *Settings {
	if s == nil {
		return defaultSettings()
	}
	dst := NewSettings()
	dst.Permissions.DefaultMode = s.Permissions.DefaultMode
	dst.Permissions.Allow = append([]string(nil), s.Permissions.Allow...)
	dst.Permissions.Deny = append([]string(nil), s.Permissions.Deny...)
	dst.Permissions.Ask = append([]string(nil), s.Permissions.Ask...)
	dst.Model = s.Model
	dst.Theme = s.Theme
	dst.SearchProvider = s.SearchProvider
	if s.AllowBypass != nil {
		v := *s.AllowBypass
		dst.AllowBypass = &v
	}
	for k, v := range s.Env {
		dst.Env[k] = v
	}
	for k, v := range s.EnabledPlugins {
		dst.EnabledPlugins[k] = v
	}
	for k, v := range s.DisabledTools {
		dst.DisabledTools[k] = v
	}
	for event, hooks := range s.Hooks {
		clonedHooks := make([]Hook, len(hooks))
		for i, hook := range hooks {
			clonedHooks[i].Matcher = hook.Matcher
			clonedHooks[i].Hooks = make([]HookCmd, len(hook.Hooks))
			for j, cmd := range hook.Hooks {
				clonedHooks[i].Hooks[j] = HookCmd{
					Type:           cmd.Type,
					Command:        cmd.Command,
					Prompt:         cmd.Prompt,
					URL:            cmd.URL,
					If:             cmd.If,
					Shell:          cmd.Shell,
					Model:          cmd.Model,
					Async:          cmd.Async,
					AsyncRewake:    cmd.AsyncRewake,
					Timeout:        cmd.Timeout,
					StatusMessage:  cmd.StatusMessage,
					Once:           cmd.Once,
					Headers:        maps.Clone(cmd.Headers),
					AllowedEnvVars: append([]string(nil), cmd.AllowedEnvVars...),
				}
			}
		}
		dst.Hooks[event] = clonedHooks
	}
	return dst
}
