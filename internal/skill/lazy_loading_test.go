package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillDiskReading(t *testing.T) {
	// Create a temporary skill directory
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	// Create SKILL.md file
	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: test-lazy-skill
description: A test skill for lazy loading verification
---

This is the full skill instructions content.

## Instructions
1. Do something
2. Do something else

## Example
` + "```bash\necho 'hello world'\n```"

	if err := os.WriteFile(skillFile, []byte(skillContent), 0o644); err != nil {
		t.Fatalf("Failed to create skill file: %v", err)
	}

	// Load the skill (simulating startup)
	loader := newLoader(tmpDir)
	skill, err := loader.loadSkillFile(skillFile, ScopeUser, "")
	if err != nil {
		t.Fatalf("Failed to load skill file: %v", err)
	}

	// Verify metadata is loaded
	if skill.Name != "test-lazy-skill" {
		t.Errorf("Name not loaded: got %q", skill.Name)
	}
	if skill.Description != "A test skill for lazy loading verification" {
		t.Errorf("Description not loaded: got %q", skill.Description)
	}

	// Call GetInstructions() to read from disk
	instructions := skill.Instructions()
	if instructions == "" {
		t.Error("GetInstructions() should return the full content")
	}
	if len(instructions) < 50 {
		t.Errorf("Instructions seems too short: %d chars", len(instructions))
	}

	// Verify modifications are immediately reflected (no caching)
	updatedContent := `---
name: test-lazy-skill
description: A test skill for lazy loading verification
---

Updated instructions with new content.
`
	if err := os.WriteFile(skillFile, []byte(updatedContent), 0o644); err != nil {
		t.Fatalf("Failed to update skill file: %v", err)
	}

	updatedInstructions := skill.Instructions()
	if !strings.Contains(updatedInstructions, "Updated instructions") {
		t.Errorf("GetInstructions() should reflect disk changes immediately, got: %s", updatedInstructions)
	}

	t.Logf("✓ Metadata loaded at startup (name=%s, desc=%s)",
		skill.Name, skill.Description[:20]+"...")
	t.Logf("✓ Instructions read from disk (%d chars)", len(instructions))
	t.Logf("✓ Disk changes reflected immediately")
}
