package mcp

import (
	"testing"
)

func TestSessionConfigsAreIsolatedAndNotPersisted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	input := map[string]ServerConfig{"client": {Command: "not-run", Env: map[string]string{"TOKEN": "test-only"}, Args: []string{"original"}}}
	first, err := NewSession(cwd, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	input["client"].Env["TOKEN"] = "changed"
	input["client"].Args[0] = "changed"
	cfg, _ := first.Config("client")
	if cfg.Env["TOKEN"] != "test-only" || cfg.Args[0] != "original" || cfg.CWD != cwd {
		t.Fatal("configuration not owned")
	}
	second, err := NewSession(cwd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Config("client"); ok {
		t.Fatal("client config persisted or leaked")
	}
	for _, name := range []string{"", "a__b", "a:b", "a b"} {
		if _, err := NewSession(cwd, nil, map[string]ServerConfig{name: {Command: "not-run"}}); err == nil {
			t.Fatalf("accepted ambiguous server name %q", name)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
}
