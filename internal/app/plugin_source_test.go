package app

import (
	"testing"

	"github.com/genai-io/san/internal/plugin"
)

func TestPluginPathScope(t *testing.T) {
	for _, tc := range []struct {
		scope   plugin.Scope
		project bool
	}{
		{plugin.ScopeUser, false}, {plugin.ScopeManaged, false},
		{plugin.ScopeProject, true}, {plugin.ScopeLocal, true},
	} {
		t.Run(tc.scope.String(), func(t *testing.T) {
			r := plugin.NewRegistry()
			r.Register(&plugin.Plugin{Manifest: plugin.Manifest{Name: "demo"}, Scope: tc.scope, Enabled: true,
				Components: plugin.Components{Skills: []string{"skills/probe"}, Commands: []string{"commands/check.md"}}})
			source := pluginSource{r}
			if p := source.skillPaths(); len(p) != 1 || p[0].IsProject != tc.project || p[0].Namespace != "demo" || p[0].Path != "skills/probe" {
				t.Errorf("skill paths = %+v; want demo:skills/probe, project=%t", p, tc.project)
			}
			if p := source.commandPaths(); len(p) != 1 || p[0].IsProject != tc.project || p[0].Namespace != "demo" || p[0].Path != "commands/check.md" {
				t.Errorf("command paths = %+v; want demo:commands/check.md, project=%t", p, tc.project)
			}
		})
	}
}
