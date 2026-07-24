package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaEmbedsDirectory(t *testing.T) {
	directory := "Available custom agents for the Agent tool:\n\n- project-reviewer: General multi-step review agent\n  Tools: Read, Bash(git diff*)\n- plugin:browser-user: Uses a browser\n  Tools: WebFetch"

	schema := agentSchema(directory)
	if !strings.Contains(schema.Description, "project-reviewer") {
		t.Error("Agent description should embed the directory body when supplied")
	}
	if !strings.Contains(schema.Description, "plugin:browser-user") {
		t.Error("Agent description should list every custom directory entry")
	}
	if !strings.Contains(schema.Description, "Set name only when selecting one of these custom agents") {
		t.Error("Agent description should retain custom-agent guidance after the directory")
	}
}

func TestAgentSchemaOmitsDirectoryWhenEmpty(t *testing.T) {
	schema := agentSchema("")
	if strings.Contains(schema.Description, "Available custom agents") {
		t.Error("empty directory should not produce an Available-agents block")
	}
	if !strings.Contains(schema.Description, "Omit name to use the default agent") {
		t.Error("default-agent guidance must remain without a directory")
	}
}

// TestAgentToolSchemaMatchesEmptyDirectory verifies the tool.Tool method and
// the directory-less builder agree, so the Agent's default self-description
// (Schema) and its zero-directory form (SchemaWithAgentDirectory) can't drift.
func TestAgentToolSchemaMatchesEmptyDirectory(t *testing.T) {
	at := &AgentTool{}
	if at.Schema().Description != agentSchema("").Description {
		t.Error("AgentTool.Schema must equal the directory-less agentSchema")
	}
	if at.SchemaWithAgentDirectory("").Description != agentSchema("").Description {
		t.Error("SchemaWithAgentDirectory(\"\") must equal the directory-less agentSchema")
	}
}

func TestAgentSchemaUsesNameWithoutSubagentType(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	if _, ok := properties["name"]; !ok {
		t.Fatal("Agent schema should expose name for custom agent selection")
	}
	if _, ok := properties["subagent_type"]; ok {
		t.Fatal("Agent schema must not expose subagent_type")
	}
}

func TestAgentSchemaModeEnumExcludesBypass(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	mode := properties["mode"].(map[string]any)
	enum := mode["enum"].([]string)
	want := []string{"explore", "edit", "default"}
	if strings.Join(enum, ",") != strings.Join(want, ",") {
		t.Fatalf("mode enum = %v, want %v", enum, want)
	}
}

func TestAgentSchemaOmitsModelOverride(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	if _, ok := properties["model"]; ok {
		t.Fatal("Agent schema should not expose a model override")
	}
}

func TestAgentStopSchemaRequiresOnlyTaskID(t *testing.T) {
	params := (&AgentStopTool{}).Schema().Parameters.(map[string]any)
	required := params["required"].([]string)
	if len(required) != 1 || required[0] != "task_id" {
		t.Fatalf("AgentStop required fields = %#v, want [task_id]", required)
	}
}
