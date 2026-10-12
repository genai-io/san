package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaIncludesPredefinedCatalogWithoutSelectors(t *testing.T) {
	catalog := "Available predefined agents:\n\n- reviewer: Reviews changes\n  Use when: Before merging"
	tool := &AgentTool{}
	schema := tool.SchemaWithAgentCatalog(catalog)
	if !strings.Contains(schema.Description, catalog) || !strings.Contains(schema.Description, "Use name to select a predefined agent") {
		t.Fatalf("predefined agents are not discoverable: %s", schema.Description)
	}
	if strings.Contains(tool.Schema().Description, "Available predefined agents") {
		t.Fatal("empty registry advertises a catalog")
	}
	if tool.SchemaWithAgentCatalog(" \n ").Description != tool.Schema().Description {
		t.Fatal("blank catalog changed the schema")
	}
	properties := schema.Definition.(map[string]any)["properties"].(map[string]any)
	for _, removed := range []string{"mode", "model"} {
		if _, exists := properties[removed]; exists {
			t.Fatalf("catalog restored %s selector", removed)
		}
	}
}
