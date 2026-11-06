package plugin

import (
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestMergeHooksIntoSettingsUsesReceiver(t *testing.T) {
	original := defaultRegistry
	t.Cleanup(func() { defaultRegistry = original })
	registry := func(matcher string) *Registry {
		r := NewRegistry()
		r.plugins[matcher] = &Plugin{Enabled: true, Components: Components{Hooks: &HooksConfig{
			Hooks: map[string][]HookMatcher{"PreToolUse": {{Matcher: matcher}}},
		}}}
		return r
	}
	defaultRegistry = registry("global")
	scoped := registry("scoped")
	settings := &setting.Data{Hooks: map[string][]setting.Hook{"PreToolUse": {{Matcher: "existing"}}}}
	scoped.MergeHooksIntoSettings(settings)
	hooks := settings.Hooks["PreToolUse"]
	if len(hooks) != 2 || hooks[0].Matcher != "existing" || hooks[1].Matcher != "scoped" {
		t.Fatalf("unexpected merged hooks: %+v", hooks)
	}
	global := &setting.Data{}
	MergePluginHooksIntoSettings(global)
	if hooks := global.Hooks["PreToolUse"]; len(hooks) != 1 || hooks[0].Matcher != "global" {
		t.Fatalf("default wrapper hooks: %+v", hooks)
	}
	empty := &setting.Data{}
	NewRegistry().MergeHooksIntoSettings(empty)
	if empty.Hooks == nil || len(empty.Hooks) != 0 {
		t.Fatalf("empty registry inherited hooks: %+v", empty.Hooks)
	}
}
