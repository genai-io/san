package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaKeepsValidationAndDenialGuidance(t *testing.T) {
	description := agentSchema("").Description
	for _, phrase := range []string{
		"Delegate when the user requests it",
		"only task-local context", "Include the validation plan",
		"Do not retry permission-denied commands", "failed tool and reason",
		"Check uncertain side effects before retrying failed work", "within the authorized scope",
	} {
		if !strings.Contains(description, phrase) {
			t.Errorf("description missing %q", phrase)
		}
	}
	properties := agentToolParameters["properties"].(map[string]any)
	if len(properties) != 5 {
		t.Fatalf("Agent parameters = %v, want creation fields only", properties)
	}
	if _, exists := properties["resume_task_id"]; exists {
		t.Fatal("Agent must only create sessions")
	}
	if !strings.Contains(description, "Use SendMessage") {
		t.Fatal("follow-up guidance missing")
	}
}
