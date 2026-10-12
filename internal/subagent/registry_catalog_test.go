package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestAgentCatalogContainsOnlyEnabledDiscoveryMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	r := NewRegistry()
	if err := r.InitStores(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(t.TempDir(), "reviewer.md")
	if err := os.WriteFile(promptPath, []byte("---\nname: reviewer\ndescription: Reviews changes\n---\nBODY_SENTINEL: full charter belongs only to the worker.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := &AgentConfig{Name: "reviewer", Description: "Reviews\nchanges", WhenToUse: "Before\nmerging", Model: "MODEL_SENTINEL", PermissionMode: PermissionBypass,
		AllowTools: ToolList{{Name: "Bash", Pattern: "COMMAND_SENTINEL"}}, SourceFile: promptPath}
	r.Register(config)
	r.Register(&AgentConfig{Name: "a-plugin:reader", Description: "Reads source"})
	r.Register(&AgentConfig{Name: "disabled", Description: "DISABLED_SENTINEL"})
	if err := r.SetEnabled("disabled", false, setting.ScopeProject); err != nil {
		t.Fatal(err)
	}
	catalog := r.AgentCatalog()
	for _, want := range []string{"Available predefined agents:", "- reviewer: Reviews changes", "Use when: Before merging", "- a-plugin:reader: Reads source"} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("catalog lost %q: %s", want, catalog)
		}
	}
	for _, omitted := range []string{"BODY_SENTINEL", "MODEL_SENTINEL", "COMMAND_SENTINEL", "DISABLED_SENTINEL", "bypassPermissions", "Tools:"} {
		if strings.Contains(catalog, omitted) {
			t.Fatalf("catalog leaked %q: %s", omitted, catalog)
		}
	}
	if strings.Index(catalog, "- a-plugin:reader") > strings.Index(catalog, "- reviewer") {
		t.Fatal("catalog ordering is unstable")
	}
	if config.SystemPrompt != "" {
		t.Fatal("discovery eagerly loaded the full charter")
	}
	r.LoadPersona([]string{"reviewer"})
	if strings.Contains(r.AgentCatalog(), "a-plugin:reader") {
		t.Fatal("persona-hidden agent remained discoverable")
	}
	if err := r.SetEnabled("reviewer", false, setting.ScopeProject); err != nil {
		t.Fatal(err)
	}
	if got := r.AgentCatalog(); got != "" {
		t.Fatalf("empty catalog should omit its heading: %q", got)
	}
}
