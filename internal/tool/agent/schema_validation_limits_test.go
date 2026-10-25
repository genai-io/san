package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaKeepsValidationAndDenialGuidance(t *testing.T) {
	description := agentSchema("").Description
	for _, phrase := range []string{"all context it needs", "Include the validation plan", "Do not retry denied commands", "failed tool and reason"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("description missing %q", phrase)
		}
	}
	properties := agentToolParameters["properties"].(map[string]any)
	if len(properties) != 6 {
		t.Fatalf("Agent parameters = %v, want prompt/description/name/background/budget/continuation", properties)
	}
	for _, phrase := range []string{"resume_task_id", "original child session", "short incremental prompt"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("continuation description missing %q", phrase)
		}
	}
}

func TestAgentSchemaDistinguishesCreationAndContinuation(t *testing.T) {
	properties := agentToolParameters["properties"].(map[string]any)
	for field, phrases := range map[string][]string{
		"prompt":            {"self-contained task brief", "only the short incremental instruction", "restored internally"},
		"run_in_background": {"Defaults to false for new work", "Continuation always runs in the background"},
		"max_steps":         {"positive value", "selected agent's configured budget", "continuation retains its original budget"},
		"resume_task_id":    {"original task identity", "permission policy", "Unknown IDs are errors"},
	} {
		description := properties[field].(map[string]any)["description"].(string)
		for _, phrase := range phrases {
			if !strings.Contains(description, phrase) {
				t.Errorf("%s description missing %q", field, phrase)
			}
		}
	}
	if _, hasDefault := properties["resume_task_id"].(map[string]any)["default"]; hasDefault {
		t.Fatal("continuation target must not default to a guessed task or placeholder")
	}
}

func TestResumeCreationSemanticsBelongToFieldDescription(t *testing.T) {
	schema := agentSchema("")
	definition := schema.Definition.(map[string]any)
	properties := definition["properties"].(map[string]any)
	field := properties["resume_task_id"].(map[string]any)
	description := field["description"].(string)
	for _, phrase := range []string{"For new work, omit this field or use null", "Existing Task ID", "not an agent name or file path"} {
		if !strings.Contains(description, phrase) {
			t.Errorf("resume field missing %q", phrase)
		}
	}
	if strings.Contains(schema.Description, "New work: omit resume_task_id") {
		t.Fatal("field-specific creation syntax duplicated in the general tool prompt")
	}
	types := field["type"].([]string)
	if strings.Join(types, ",") != "string,null" {
		t.Fatalf("optional resume target must accept null: %v", types)
	}
}
