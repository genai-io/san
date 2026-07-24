package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentLazyLoading(t *testing.T) {
	// Create a temporary agent file
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "test-agent.md")

	agentContent := `---
name: test-lazy-agent
description: A test agent for lazy loading verification
model: opus
---

This is the full system prompt content that should NOT be loaded at startup.
It contains detailed instructions for the agent.

## Instructions
1. Do something
2. Do something else
`
	if err := os.WriteFile(agentFile, []byte(agentContent), 0o644); err != nil {
		t.Fatalf("Failed to create test agent file: %v", err)
	}

	// Parse the agent file (simulating startup)
	config, err := parseAgentFile(agentFile)
	if err != nil {
		t.Fatalf("Failed to parse agent file: %v", err)
	}

	// Verify metadata is loaded
	if config.Name != "test-lazy-agent" {
		t.Errorf("Name not loaded: got %q", config.Name)
	}
	if config.Description != "A test agent for lazy loading verification" {
		t.Errorf("Description not loaded: got %q", config.Description)
	}
	if config.Model != "opus" {
		t.Errorf("Model not loaded: got %q", config.Model)
	}

	// Verify SystemPrompt is NOT loaded at parse time (lazy loading)
	if config.SystemPrompt != "" {
		t.Errorf("SystemPrompt should be empty at parse time, got: %q", config.SystemPrompt)
	}

	// Now call GetSystemPrompt() to trigger lazy loading
	prompt := config.GetSystemPrompt()

	// Verify the full content is now loaded
	if prompt == "" {
		t.Error("GetSystemPrompt() should return the full content")
	}
	if config.SystemPrompt == "" {
		t.Error("SystemPrompt should be populated after GetSystemPrompt()")
	}

	// Verify it contains the expected content
	if len(prompt) < 50 {
		t.Errorf("System prompt seems too short: %d chars", len(prompt))
	}

	t.Logf("✓ Metadata loaded at startup (name=%s, desc=%s, model=%s)",
		config.Name, config.Description[:20]+"...", config.Model)
	t.Logf("✓ SystemPrompt loaded lazily (%d chars)", len(prompt))
}
