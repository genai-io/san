package subagent

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestAgentLoadersSharePrecedenceAndIsolatePluginPaths(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	original := defaultRegistry
	SetDefaultRegistry(NewRegistry())
	t.Cleanup(func() { SetDefaultRegistry(original); ClearPluginAgentPaths() })
	ClearPluginAgentPaths()
	paths := []string{
		filepath.Join(cwd, ".san", "agents"),
		filepath.Join(home, ".san", "agents"),
		filepath.Join(cwd, ".claude", "agents"),
		filepath.Join(home, ".claude", "agents"),
		t.TempDir(), t.TempDir(),
	}
	// Each name starts at a different priority, testing every adjacent pair.
	for i, path := range paths {
		for name := 0; name <= i; name++ {
			writeAgentFile(t, path, fmt.Sprintf("agent%d", name), fmt.Sprintf("description: priority%d", i))
		}
	}
	var plugins []PluginAgentPath
	for _, path := range paths[4:] {
		plugins = append(plugins, PluginAgentPath{Path: path})
		AddPluginAgentPath(path, "")
	}
	globalOnly := t.TempDir()
	writeAgentFile(t, globalOnly, "global-only", "description: global")
	AddPluginAgentPath(globalOnly, "plugin")
	LoadAgents(cwd)
	scoped, err := Load(cwd, plugins)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Registry{defaultRegistry, scoped} {
		for i := range paths {
			config, ok := r.Get(fmt.Sprintf("agent%d", i))
			if !ok || config.Description != fmt.Sprintf("priority%d", i) {
				t.Fatalf("priority %d: got %+v, found %v", i, config, ok)
			}
		}
	}
	if _, ok := defaultRegistry.Get("plugin:global-only"); !ok {
		t.Fatal("default loader ignored global plugin path")
	}
	if _, ok := scoped.Get("plugin:global-only"); ok {
		t.Fatal("scoped loader inherited global plugin path")
	}
}
