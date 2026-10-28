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
