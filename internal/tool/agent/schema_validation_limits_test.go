package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaDisclosesEditValidationLimits(t *testing.T) {
	for _, directory := range []string{"", "Editor: modifies files"} {
		schema := agentSchema(directory)
		for _, phrase := range []string{"read-only shell", "tests/build require an explicit allowed command or parent validation", "Do not retry denied commands"} {
			if !strings.Contains(schema.Description, phrase) {
				t.Errorf("description missing %q", phrase)
			}
		}
	}
	properties := agentToolParameters["properties"].(map[string]any)
	mode := properties["mode"].(map[string]any)["description"].(string)
	if !strings.Contains(mode, "tests/build require an explicit allowed command or parent validation") {
		t.Fatalf("mode does not disclose validation limits: %s", mode)
	}
}
