package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillStateNextState(t *testing.T) {
	tests := []struct {
		current  SkillState
		expected SkillState
	}{
		{StateDisable, StateEnable},
		{StateEnable, StateActive},
		{StateActive, StateDisable},
	}

	for _, tc := range tests {
		result := tc.current.NextState()
		if result != tc.expected {
			t.Errorf("NextState(%s) = %s, want %s", tc.current, result, tc.expected)
		}
	}
}

func TestSkillFullName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		expected  string
	}{
		{"commit", "git", "git:commit"},
		{"my-issues", "jira", "jira:my-issues"},
		{"test-skill", "", "test-skill"},
	}

	for _, tc := range tests {
		skill := &Skill{Name: tc.name, Namespace: tc.namespace}
		result := skill.FullName()
		if result != tc.expected {
			t.Errorf("FullName(%s, %s) = %s, want %s", tc.name, tc.namespace, result, tc.expected)
		}
	}
}

func TestLoadSkillFile(t *testing.T) {
	// Create a temporary skill file
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: test-skill
description: A test skill
allowed-tools: [Read, Grep]
argument-hint: "[message]"
---

# Test Skill Instructions

This is the skill content.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := newLoader(tmpDir)
	skill, err := loader.loadSkillFile(skillPath, ScopeUser, "")
	if err != nil {
		t.Fatalf("loadSkillFile failed: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("Name = %s, want test-skill", skill.Name)
	}
	if skill.Description != "A test skill" {
		t.Errorf("Description = %s, want 'A test skill'", skill.Description)
	}
	if skill.ArgumentHint != "[message]" {
		t.Errorf("ArgumentHint = %s, want '[message]'", skill.ArgumentHint)
	}
	if len(skill.AllowedTools) != 2 {
		t.Errorf("AllowedTools length = %d, want 2", len(skill.AllowedTools))
	}
	if skill.Scope != ScopeUser {
		t.Errorf("Scope = %d, want ScopeUser", skill.Scope)
	}
}

func TestLoadAllSkills(t *testing.T) {
	// Create temporary directories for skills
	tmpDir := t.TempDir()

	// Create a skill in the test directory
	skillDir := filepath.Join(tmpDir, ".san", "skills", "example-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: example-skill
description: An example skill
---

Example instructions.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create loader with the temp directory as project root
	loader := &loader{
		cwd: tmpDir,
	}

	skills, err := loader.loadAll()
	if err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	skill, ok := skills["example-skill"]
	if !ok {
		t.Fatal("example-skill not found in loaded skills")
	}

	if skill.Description != "An example skill" {
		t.Errorf("Description = %s, want 'An example skill'", skill.Description)
	}
}

func TestLoadSkillWithNamespace(t *testing.T) {
	// Create temporary directories for skills
	tmpDir := t.TempDir()

	// Create a namespaced skill
	skillDir := filepath.Join(tmpDir, ".san", "skills", "commit")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: commit
namespace: git
description: Create git commits
argument-hint: "[message]"
---

Commit instructions.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create loader with the temp directory as project root
	loader := &loader{
		cwd: tmpDir,
	}

	skills, err := loader.loadAll()
	if err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	// Skill should be keyed by FullName (git:commit)
	skill, ok := skills["git:commit"]
	if !ok {
		t.Fatal("git:commit not found in loaded skills")
	}

	if skill.Name != "commit" {
		t.Errorf("Name = %s, want 'commit'", skill.Name)
	}
	if skill.Namespace != "git" {
		t.Errorf("Namespace = %s, want 'git'", skill.Namespace)
	}
	if skill.FullName() != "git:commit" {
		t.Errorf("FullName = %s, want 'git:commit'", skill.FullName())
	}
}

func TestSkillRegistry(t *testing.T) {
	// Create temporary directories for skills
	tmpDir := t.TempDir()

	// Create a skill in the test directory
	skillDir := filepath.Join(tmpDir, ".san", "skills", "registry-test")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: registry-test
description: Registry test skill
---

Test instructions.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Override the loader to use our temp directory
	loader := &loader{
		cwd: tmpDir,
	}

	skills, err := loader.loadAll()
	if err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	// Create mock stores in temp dir
	userStorePath := filepath.Join(tmpDir, "user-skills.json")
	projectStorePath := filepath.Join(tmpDir, "project-skills.json")
	userStore := &Store{
		path:   userStorePath,
		states: make(map[string]SkillState),
	}
	projectStore := &Store{
		path:   projectStorePath,
		states: make(map[string]SkillState),
	}

	registry := &Registry{
		skills:       skills,
		userStore:    userStore,
		projectStore: projectStore,
		cwd:          tmpDir,
	}

	// Test Get
	skill, ok := registry.Get("registry-test")
	if !ok {
		t.Fatal("registry-test not found")
	}
	if skill.State != StateEnable {
		t.Errorf("Default state = %s, want StateEnable", skill.State)
	}

	// Test SetState (to user level)
	err = registry.SetState("registry-test", StateActive, true)
	if err != nil {
		t.Fatalf("SetState failed: %v", err)
	}
	if skill.State != StateActive {
		t.Errorf("State after SetState = %s, want StateActive", skill.State)
	}

	// Test GetActive
	activeSkills := registry.GetActive()
	if len(activeSkills) != 1 {
		t.Errorf("GetActive returned %d skills, want 1", len(activeSkills))
	}

	// Test GetSkillsSection
	prompt := registry.GetSkillsSection()
	if prompt == "" {
		t.Error("GetSkillsSection returned empty string for active skill")
	}
	if !contains(prompt, "registry-test") {
		t.Error("Prompt should contain skill name")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLoadPluginSkills(t *testing.T) {
	// Create temporary directories for plugin skills
	tmpDir := t.TempDir()

	// Create a plugin cache directory with skills
	pluginCacheDir := filepath.Join(tmpDir, ".san", "plugins", "cache", "test-marketplace", "git", "1.0.0")
	skillDir := filepath.Join(pluginCacheDir, "skills", "commit")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: commit
description: Create git commits
argument-hint: "[message]"
---

Git commit instructions.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create installed_plugins.json
	pluginsDir := filepath.Join(tmpDir, ".san", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	installedPlugins := installedPluginsData{
		Version: 2,
		Plugins: map[string][]pluginInstall{
			"git@test-marketplace": {
				{
					Scope:       "user",
					InstallPath: pluginCacheDir,
					Version:     "1.0.0",
				},
			},
		},
	}

	configData, _ := json.MarshalIndent(installedPlugins, "", "  ")
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), configData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create loader with the temp directory as project root
	loader := &loader{
		cwd: tmpDir,
	}

	skills, err := loader.loadAll()
	if err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	// Skill should inherit namespace from plugin name (git)
	skill, ok := skills["git:commit"]
	if !ok {
		t.Fatal("git:commit not found in loaded skills - namespace inheritance may not be working")
	}

	if skill.Name != "commit" {
		t.Errorf("Name = %s, want 'commit'", skill.Name)
	}
	if skill.Namespace != "git" {
		t.Errorf("Namespace = %s, want 'git' (inherited from plugin name)", skill.Namespace)
	}
	if skill.FullName() != "git:commit" {
		t.Errorf("FullName = %s, want 'git:commit'", skill.FullName())
	}
	if skill.Scope != ScopeProjectPlugin {
		t.Errorf("Scope = %s, want ScopeProjectPlugin", skill.Scope.String())
	}
}

func TestPluginSkillExplicitNamespaceOverride(t *testing.T) {
	// Create temporary directories for plugin skills
	tmpDir := t.TempDir()

	// Create a plugin cache directory with skills
	pluginCacheDir := filepath.Join(tmpDir, ".san", "plugins", "cache", "test-marketplace", "my-plugin", "1.0.0")
	skillDir := filepath.Join(pluginCacheDir, "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := `---
name: review
namespace: code
description: Code review skill
---

Review instructions.
`
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create installed_plugins.json
	pluginsDir := filepath.Join(tmpDir, ".san", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	installedPlugins := installedPluginsData{
		Version: 2,
		Plugins: map[string][]pluginInstall{
			"my-plugin@test-marketplace": {
				{
					Scope:       "user",
					InstallPath: pluginCacheDir,
					Version:     "1.0.0",
				},
			},
		},
	}

	configData, _ := json.MarshalIndent(installedPlugins, "", "  ")
	if err := os.WriteFile(filepath.Join(pluginsDir, "installed_plugins.json"), configData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create loader with the temp directory as project root
	loader := &loader{
		cwd: tmpDir,
	}

	skills, err := loader.loadAll()
	if err != nil {
		t.Fatalf("loadAll failed: %v", err)
	}

	// Skill should use explicit namespace (code) not plugin name (my-plugin)
	skill, ok := skills["code:review"]
	if !ok {
		t.Fatal("code:review not found in loaded skills - explicit namespace should override plugin name")
	}

	if skill.Namespace != "code" {
		t.Errorf("Namespace = %s, want 'code' (explicit frontmatter)", skill.Namespace)
	}
}
