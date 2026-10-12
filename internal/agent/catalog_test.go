package agent

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/register"
)

func TestMainSchemasDiscoverConfiguredAgentsAndRefreshCatalog(t *testing.T) {
	r := subagent.NewRegistry()
	r.Register(&subagent.AgentConfig{Name: "reviewer", Description: "Reviews changes"})
	params := BuildParams{AgentCatalog: r.AgentCatalog}
	description := func() string {
		for _, schema := range params.Schemas() {
			if schema.Name == tool.ToolAgent {
				return schema.Description
			}
		}
		t.Fatal("Agent schema missing")
		return ""
	}
	if got := description(); !strings.Contains(got, "Available predefined agents:") || !strings.Contains(got, "- reviewer: Reviews changes") {
		t.Fatalf("main model cannot discover configured agent: %s", got)
	}
	r.Register(&subagent.AgentConfig{Name: "test-runner", Description: "Runs tests"})
	if got := description(); !strings.Contains(got, "- test-runner: Runs tests") {
		t.Fatalf("catalog did not refresh: %s", got)
	}
	r.LoadPersona([]string{"reviewer"})
	if got := description(); strings.Contains(got, "test-runner") {
		t.Fatalf("main model sees persona-hidden agent: %s", got)
	}
	calls := 0
	child := tool.Set{IsAgent: true, AgentCatalog: func() string { calls++; return r.AgentCatalog() }}
	for _, schema := range child.Tools() {
		if schema.Name == tool.ToolAgent {
			t.Fatal("worker can recursively spawn agents")
		}
	}
	if calls != 0 {
		t.Fatal("worker eagerly rendered a main-only catalog")
	}
}
