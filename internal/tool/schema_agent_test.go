package tool

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestAgentToolSchemaEmbedsDirectory(t *testing.T) {
	directory := "Available agent types for the Agent tool:\n\n- general-purpose: General multi-step agent\n  Tools: *\n- code-reviewer: Reviews code changes\n  Tools: Read, Glob, Grep"

	schema := agentToolSchema(directory)
	if !strings.Contains(schema.Description, "general-purpose") {
		t.Error("Agent description should embed the directory body when supplied")
	}
	if !strings.Contains(schema.Description, "code-reviewer") {
		t.Error("Agent description should list every directory entry")
	}
	if !strings.Contains(schema.Description, "When using the Agent tool, specify a subagent_type") {
		t.Error("Agent description should retain the usage guidance after the directory")
	}
}

func TestAgentToolSchemaOmitsDirectoryWhenEmpty(t *testing.T) {
	schema := agentToolSchema("")
	if strings.Contains(schema.Description, "Available agent types") {
		t.Error("empty directory should not produce an Available-agents block")
	}
	if !strings.Contains(schema.Description, "When using the Agent tool, specify a subagent_type") {
		t.Error("usage guidance must remain even without a directory")
	}
}

func TestSubagentSchemasDoNotExposeWorktreeIsolation(t *testing.T) {
	for _, schema := range []core.ToolSchema{agentToolSchema(""), sendMessageToolSchema} {
		params := schema.Parameters.(map[string]any)
		properties := params["properties"].(map[string]any)
		if _, ok := properties["isolation"]; ok {
			t.Fatalf("%s schema must not let subagents create worktrees", schema.Name)
		}
	}
}

func TestSubagentSchemasDoNotSuggestProviderSpecificModelAliases(t *testing.T) {
	for _, schema := range []core.ToolSchema{agentToolSchema(""), sendMessageToolSchema} {
		params := schema.Parameters.(map[string]any)
		properties := params["properties"].(map[string]any)
		model := properties["model"].(map[string]any)
		if _, ok := model["enum"]; ok {
			t.Fatalf("%s model override must not suggest provider-specific aliases", schema.Name)
		}
	}
}

func TestGetToolSchemasUsesDirectoryGetter(t *testing.T) {
	schemas := GetToolSchemasWith(SchemaOptions{
		AgentDirectory: func() string {
			return "Available agent types for the Agent tool:\n\n- explorer: Read-only research"
		},
	})

	var found bool
	for _, s := range schemas {
		if s.Name != "Agent" {
			continue
		}
		found = true
		if !strings.Contains(s.Description, "explorer: Read-only research") {
			t.Errorf("Agent schema description missing directory entry, got: %s", s.Description)
		}
	}
	if !found {
		t.Fatal("Agent schema not found in GetToolSchemasWith output")
	}
}

// TestAgentDirectoryReevaluatedPerCall verifies that the AgentDirectory
// getter is invoked on each schema build, so that toggling /agents (which
// mutates the shared store) produces an updated tool description on the
// next agent rebuild — a regression here would mean enable/disable settings
// are silently ignored.
func TestAgentDirectoryReevaluatedPerCall(t *testing.T) {
	directory := "v1"
	getter := func() string { return directory }

	first := GetToolSchemasWith(SchemaOptions{AgentDirectory: getter})
	directory = "v2"
	second := GetToolSchemasWith(SchemaOptions{AgentDirectory: getter})

	pickAgent := func(schemas []core.ToolSchema) core.ToolSchema {
		for _, s := range schemas {
			if s.Name == "Agent" {
				return s
			}
		}
		t.Fatal("Agent schema not present")
		return core.ToolSchema{}
	}

	if !strings.Contains(pickAgent(first).Description, "v1") {
		t.Error("first build should embed directory v1")
	}
	if !strings.Contains(pickAgent(second).Description, "v2") {
		t.Error("second build should embed directory v2 (getter must run each call)")
	}
	if strings.Contains(pickAgent(second).Description, "v1") {
		t.Error("second build should NOT contain stale v1 directory")
	}
}

func TestBuiltInToolSchemasAreOpenAICompatibleObjects(t *testing.T) {
	for _, schema := range GetToolSchemas() {
		params, ok := schema.Parameters.(map[string]any)
		if !ok {
			t.Fatalf("%s parameters must be a JSON schema object map", schema.Name)
		}
		if typ, _ := params["type"].(string); typ != "object" {
			t.Fatalf("%s parameters must declare top-level type object, got %v", schema.Name, params["type"])
		}
		for _, keyword := range []string{"oneOf", "anyOf", "allOf", "enum", "not"} {
			if _, exists := params[keyword]; exists {
				t.Fatalf("%s parameters must not use top-level %q", schema.Name, keyword)
			}
		}
	}
}

func TestAskUserQuestionSchemaRejectsEmptyQuestionsShape(t *testing.T) {
	params := askUserQuestionToolSchema.Parameters.(map[string]any)
	if got, ok := params["minProperties"].(int); !ok || got != 1 {
		t.Fatalf("AskUserQuestion must require at least one input property, got %#v", params["minProperties"])
	}

	properties := params["properties"].(map[string]any)
	questions := properties["questions"].(map[string]any)
	if got, ok := questions["minItems"].(int); !ok || got != 1 {
		t.Fatalf("AskUserQuestion questions must require at least one item, got %#v", questions["minItems"])
	}
	if got, ok := questions["maxItems"].(int); !ok || got != 8 {
		t.Fatalf("AskUserQuestion questions must allow at most eight items, got %#v", questions["maxItems"])
	}

	item := questions["items"].(map[string]any)
	itemProps := item["properties"].(map[string]any)
	options := itemProps["options"].(map[string]any)
	if got, ok := options["minItems"].(int); !ok || got != 2 {
		t.Fatalf("AskUserQuestion nested options must require at least two items, got %#v", options["minItems"])
	}
	if got, ok := options["maxItems"].(int); !ok || got != 8 {
		t.Fatalf("AskUserQuestion nested options must allow at most eight items, got %#v", options["maxItems"])
	}
}
