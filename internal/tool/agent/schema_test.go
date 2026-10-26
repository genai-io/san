package agent

import (
	"strings"
	"testing"
)

func TestAgentToolSchemaMatchesBuilder(t *testing.T) {
	if got := (&AgentTool{}).Schema().Description; got != agentSchema("").Description {
		t.Fatalf("Agent schema differs from its builder: %q", got)
	}
	for _, removed := range []string{"Available agent definitions", "Select explore", "Select edit", "YOLO", "General"} {
		if strings.Contains(agentSchema("").Description, removed) {
			t.Fatalf("Agent description still exposes worker variants: %q", removed)
		}
	}
}

func TestAgentSchemaEncouragesDirectWorkForClearScope(t *testing.T) {
	description := agentSchema("").Description
	for _, want := range []string{
		"Handle the task directly by default",
		"independently scoped task",
		"parallel execution, specialized capabilities, or context isolation",
		"outweigh the coordination cost and loss of shared context",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("Agent description should contain %q", want)
		}
	}
}

func TestAgentSchemaRetainsDelegationGuidance(t *testing.T) {
	description := agentSchema("").Description
	for _, want := range []string{
		"all context it needs",
		"Include the validation plan",
		"report the failed tool and reason",
		"When delegating multiple independent tasks, launch them concurrently",
		"use foreground execution when you need the result to proceed",
		"background execution when you can continue independent work",
		"Do not duplicate delegated work",
		"Verify the result before reporting it",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("Agent description should retain %q guidance", want)
		}
	}
}

func TestAgentBackgroundGuidancePreservesContinuation(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	background := properties["run_in_background"].(map[string]any)
	description := background["description"].(string)
	for _, want := range []string{"Defaults to false for new work", "useful independent work", "Continuation always runs in the background"} {
		if !strings.Contains(description, want) {
			t.Errorf("background description should contain %q", want)
		}
	}
}

func TestAgentSchemaExplainsNameResolution(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	name, ok := properties["name"].(map[string]any)
	if !ok {
		t.Fatal("Agent schema should expose name")
	}
	want := "Optional name for this agent. New names label the task; an existing custom definition supplies its configuration."
	if description := name["description"]; description != want {
		t.Fatalf("name description = %q, want %q", description, want)
	}
}

func TestAgentSchemaOmitsPermissionMode(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	if _, exists := properties["mode"]; exists {
		t.Fatal("Agent schema still exposes a permission selector")
	}
}

func TestAgentSchemaOmitsModelOverride(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	if _, ok := properties["model"]; ok {
		t.Fatal("Agent schema should not expose a model override")
	}
}

func TestAgentStopSchemaRequiresOnlyTaskID(t *testing.T) {
	params := (&AgentStopTool{}).Schema().Definition.(map[string]any)
	required := params["required"].([]string)
	if len(required) != 1 || required[0] != "task_id" {
		t.Fatalf("AgentStop required fields = %#v, want [task_id]", required)
	}
}
