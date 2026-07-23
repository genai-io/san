package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaUsesSoleDefaultAgent(t *testing.T) {
	schema := (&AgentTool{}).Schema()
	if !strings.Contains(schema.Description, "default general-purpose subagent") {
		t.Fatal("Agent schema should describe the sole default subagent")
	}
	properties := agentToolParameters["properties"].(map[string]any)
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
