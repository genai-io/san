package app

import (
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/plugin"
	"github.com/genai-io/san/internal/setting"
)

// A plugin's hooks have to survive a reload. Both reload paths used to merge
// them into a throwaway Settings.Snapshot() and then hand the engine a second,
// unmerged snapshot — so after any cwd change or plugin install the plugin's
// hooks stopped firing, silently and permanently for that session.
func TestSyncSettingsToHookEngineCarriesPluginHooks(t *testing.T) {
	restore := installPluginWithPreToolUseHook(t)
	defer restore()

	m := &model{
		services: services{
			Setting: settingsWithNoHooks(t),
			Hook:    hook.NewEngine(nil, "", "", ""),
		},
	}

	m.syncSettingsToHookEngine()

	if !m.services.Hook.HasHooks(hook.PreToolUse) {
		t.Error("the engine received settings without the plugin's PreToolUse hook; " +
			"it would never fire again after a reload")
	}
}

// Guards the reason the old code failed: every Snapshot() is an independent
// deep copy, so merging into one and passing another loses the merge.
func TestSettingsSnapshotIsIndependentPerCall(t *testing.T) {
	s := settingsWithNoHooks(t)

	first := s.Snapshot()
	first.Hooks = map[string][]setting.Hook{"PreToolUse": {{Matcher: "*"}}}

	if second := s.Snapshot(); len(second.Hooks) != 0 {
		t.Error("Snapshot() shares state between calls; the merge-then-pass-another " +
			"pattern would silently start working and mask the real contract")
	}
}

// settingsWithNoHooks is the zero value deliberately: it isolates the test from
// whatever hooks the developer's own ~/.san and .san define, so the only hook
// the engine can see is the plugin's.
func settingsWithNoHooks(t *testing.T) *setting.Settings {
	t.Helper()
	s := &setting.Settings{}
	if len(s.Snapshot().Hooks) != 0 {
		t.Fatal("the zero-value Settings should carry no hooks")
	}
	return s
}

// installPluginWithPreToolUseHook registers an enabled plugin contributing one
// PreToolUse hook, and returns a function restoring the previous registry.
func installPluginWithPreToolUseHook(t *testing.T) func() {
	t.Helper()
	reg := plugin.NewRegistry()
	reg.Register(&plugin.Plugin{
		Manifest: plugin.Manifest{Name: "hooky"},
		Enabled:  true,
		Components: plugin.Components{
			Hooks: &plugin.HooksConfig{
				Hooks: map[string][]plugin.HookMatcher{
					"PreToolUse": {{Matcher: "Bash"}},
				},
			},
		},
	})
	plugin.SetDefaultRegistry(reg)
	return func() { plugin.SetDefaultRegistry(nil) }
}
