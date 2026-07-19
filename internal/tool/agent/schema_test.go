package agent

import (
	"strings"
	"testing"
)

func TestAgentSchemaEmbedsDirectory(t *testing.T) {
	directory := "Available agent types for the Agent tool:\n\n- general-purpose: General multi-step agent\n  Tools: *\n- code-reviewer: Reviews code changes\n  Tools: Read, Glob, Grep"

	schema := agentSchema(directory)
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

func TestAgentSchemaOmitsDirectoryWhenEmpty(t *testing.T) {
	schema := agentSchema("")
	if strings.Contains(schema.Description, "Available agent types") {
		t.Error("empty directory should not produce an Available-agents block")
	}
	if !strings.Contains(schema.Description, "When using the Agent tool, specify a subagent_type") {
		t.Error("usage guidance must remain even without a directory")
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
