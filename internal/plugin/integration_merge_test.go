package plugin

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/hook"
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

func TestPluginHooksUseTheirOwnRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX command hook shell")
	}
	r := NewRegistry()
	cwd := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		r.plugins[name] = &Plugin{Path: filepath.Join(cwd, name), Enabled: true, Components: Components{Hooks: &HooksConfig{
			Hooks: map[string][]HookMatcher{"SessionStart": {{Hooks: []setting.HookCmd{{
				Type: "command", Command: `printf '%s|%s' "$SAN_PLUGIN_ROOT" "$CLAUDE_PLUGIN_ROOT"`,
			}}}}},
		}}}
	}
	settings := setting.NewData()
	r.MergeHooksIntoSettings(settings)
	e := hook.NewEngine(settings, "test", cwd, "")
	t.Cleanup(e.Shutdown)
	e.SetEnvProvider(func(ctx context.Context) []string {
		return PluginEnv(WithRoot(WithRegistry(ctx, r), filepath.Join(cwd, "other plugin")))
	})
	got := e.Execute(t.Context(), hook.SessionStart, hook.HookInput{Source: "startup"})
	for _, name := range []string{"alpha", "beta"} {
		path := filepath.Join(cwd, name)
		if got.Error != nil || !strings.Contains(got.AdditionalContext, path+"|"+path) {
			t.Fatalf("hook did not receive its root: %+v", got)
		}
	}
}
