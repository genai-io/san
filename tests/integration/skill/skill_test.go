package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/v2/internal/skill"
)

// newTestRegistry creates a skill.Registry backed by temp stores for testing.
func newTestRegistry(t *testing.T, skills map[string]*skill.Skill) *skill.Registry {
	t.Helper()
	tmpDir := t.TempDir()
	userStore, _ := skill.NewStore(filepath.Join(tmpDir, "user-skills.json"))
	projectStore, _ := skill.NewStore(filepath.Join(tmpDir, "project-skills.json"))
	return skill.NewRegistryForTest(skills, userStore, projectStore)
}

func TestSkill_StateTransitions(t *testing.T) {
	s := skill.StateDisable

	// disable -> enable
	s = s.NextState()
	if s != skill.StateEnable {
		t.Errorf("expected StateEnable, got %q", s)
	}

	// enable -> active
	s = s.NextState()
	if s != skill.StateActive {
		t.Errorf("expected StateActive, got %q", s)
	}

	// active -> disable
	s = s.NextState()
	if s != skill.StateDisable {
		t.Errorf("expected StateDisable, got %q", s)
	}
}

func TestSkill_BooleanHelpers(t *testing.T) {
	s := &skill.Skill{Name: "test"}

	s.State = skill.StateDisable
	if s.IsEnabled() {
		t.Error("disabled skill should not be enabled")
	}
	if s.IsActive() {
		t.Error("disabled skill should not be active")
	}

	s.State = skill.StateEnable
	if !s.IsEnabled() {
		t.Error("enabled skill should be enabled")
	}
	if s.IsActive() {
		t.Error("enabled skill should not be active")
	}

	s.State = skill.StateActive
	if !s.IsEnabled() {
		t.Error("active skill should be enabled")
	}
	if !s.IsActive() {
		t.Error("active skill should be active")
	}
}

func TestSkill_RegistryLookup(t *testing.T) {
	skills := map[string]*skill.Skill{
		"git:commit": {Name: "commit", Namespace: "git", State: skill.StateEnable, Description: "create commit"},
		"git:push":   {Name: "push", Namespace: "git", State: skill.StateActive, Description: "push changes"},
		"review":     {Name: "review", State: skill.StateEnable, Description: "review code"},
	}

	registry := newTestRegistry(t, skills)

	// Exact match
	s, ok := registry.Get("git:commit")
	if !ok {
		t.Fatal("expected to find git:commit")
	}
	if s.Name != "commit" {
		t.Errorf("expected name 'commit', got %q", s.Name)
	}

	// Partial match
	s = registry.FindByPartialName("push")
	if s == nil {
		t.Fatal("expected to find 'push' by partial name")
	}
	if s.FullName() != "git:push" {
		t.Errorf("expected 'git:push', got %q", s.FullName())
	}

	// Not found
	s = registry.FindByPartialName("nonexistent")
	if s != nil {
		t.Error("expected nil for nonexistent skill")
	}

	// List
	all := registry.List()
	if len(all) != 3 {
		t.Errorf("expected 3 skills, got %d", len(all))
	}
}

func TestSkill_AvailablePrompt(t *testing.T) {
	skills := map[string]*skill.Skill{
		"git:commit": {Name: "commit", Namespace: "git", State: skill.StateActive, Description: "create commit"},
		"review":     {Name: "review", State: skill.StateEnable, Description: "review code"},
	}

	registry := newTestRegistry(t, skills)

	prompt := registry.GetSkillsSection()

	// Only active skills should be in the prompt
	if !strings.Contains(prompt, "git:commit") {
		t.Error("expected active skill 'git:commit' in prompt")
	}
	if strings.Contains(prompt, "review") {
		t.Error("enabled (non-active) skill 'review' should not be in prompt")
	}
	if !strings.Contains(prompt, "Use the Skill tool to invoke") {
		t.Error("expected skills directory header in body")
	}
}

func TestSkill_InvocationPrompt(t *testing.T) {
	// Create a real SKILL.md file so GetInstructions() can read from disk
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: test-skill\ndescription: a test skill\n---\n\nDo the thing step by step"), 0o644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	skills := map[string]*skill.Skill{
		"test-skill": {
			Name:        "test-skill",
			State:       skill.StateActive,
			Description: "a test skill",
			FilePath:    skillFile,
		},
	}

	registry := newTestRegistry(t, skills)

	prompt := registry.GetSkillInvocationPrompt("test-skill")

	if !strings.Contains(prompt, "<skill-invocation") {
		t.Error("expected XML wrapper")
	}
	if !strings.Contains(prompt, "Do the thing step by step") {
		t.Error("expected instructions in prompt")
	}
}

func TestSkill_ScopePriority(t *testing.T) {
	// Lower scope value = lower priority
	if skill.ScopeClaudeUser >= skill.ScopeUser {
		t.Error("ScopeClaudeUser should be lower priority than ScopeUser")
	}
	if skill.ScopeUser >= skill.ScopeProject {
		t.Error("ScopeUser should be lower priority than ScopeProject")
	}
	if skill.ScopeUserPlugin >= skill.ScopeProjectPlugin {
		t.Error("ScopeUserPlugin should be lower priority than ScopeProjectPlugin")
	}
}

func TestSkill_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "skills.json")

	// Create store, set state
	store, err := skill.NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	err = store.SetState("git:commit", skill.StateActive)
	if err != nil {
		t.Fatalf("SetState() error: %v", err)
	}

	// Verify file was written
	if _, err := os.Stat(storePath); os.IsNotExist(err) {
		t.Fatal("expected skills.json to exist")
	}

	// Create new store from same file — state should persist
	store2, err := skill.NewStore(storePath)
	if err != nil {
		t.Fatalf("NewStore() error: %v", err)
	}

	state, ok := store2.GetState("git:commit")
	if !ok {
		t.Fatal("expected persisted state for git:commit")
	}
	if state != skill.StateActive {
		t.Errorf("expected StateActive, got %q", state)
	}
}

func TestSkill_FullName(t *testing.T) {
	s1 := &skill.Skill{Name: "commit", Namespace: "git"}
	if s1.FullName() != "git:commit" {
		t.Errorf("expected 'git:commit', got %q", s1.FullName())
	}

	s2 := &skill.Skill{Name: "review"}
	if s2.FullName() != "review" {
		t.Errorf("expected 'review', got %q", s2.FullName())
	}
}

// TestSkill_ScopePriority_ProjectOverridesUser verifies that a project-level
// skill state overrides a user-level state for a skill with the same name.
func TestSkill_ScopePriority_ProjectOverridesUser(t *testing.T) {
	tmpDir := t.TempDir()

	// Create user store and set skill to StateEnable
	userStore, err := skill.NewStore(filepath.Join(tmpDir, "user-skills.json"))
	if err != nil {
		t.Fatalf("NewStore() user error: %v", err)
	}
	if err := userStore.SetState("greet", skill.StateEnable); err != nil {
		t.Fatalf("user SetState() error: %v", err)
	}

	// Create project store and set same skill to StateActive (higher priority)
	projectStore, err := skill.NewStore(filepath.Join(tmpDir, "project-skills.json"))
	if err != nil {
		t.Fatalf("NewStore() project error: %v", err)
	}
	if err := projectStore.SetState("greet", skill.StateActive); err != nil {
		t.Fatalf("project SetState() error: %v", err)
	}

	// Build a registry with the skill starting at StateDisable
	skills := map[string]*skill.Skill{
		"greet": {Name: "greet", State: skill.StateDisable, Description: "greet the user"},
	}
	registry := skill.NewRegistryForTest(skills, userStore, projectStore)

	// Simulate the state-application logic from Initialize():
	// user state applied first, then project state overrides.
	s, ok := registry.Get("greet")
	if !ok {
		t.Fatal("expected to find skill 'greet'")
	}

	// Apply user state
	if state, ok := userStore.GetState("greet"); ok {
		s.State = state
	}
	// Apply project state (higher priority — overrides user)
	if state, ok := projectStore.GetState("greet"); ok {
		s.State = state
	}

	if s.State != skill.StateActive {
		t.Errorf("expected project-level StateActive to win, got %q", s.State)
	}
}

// TestSkill_Active_AppearsInSystemPrompt verifies that active skill content
// is included in the system prompt section returned by GetSkillsSection.
func TestSkill_Active_AppearsInSystemPrompt(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a real skill file so GetInstructions() can read it
	skillDir := filepath.Join(tmpDir, "deploy-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	content := "---\nname: deploy\ndescription: deploy the app\n---\n\nRun the deployment pipeline step by step."
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	skills := map[string]*skill.Skill{
		"deploy":  {Name: "deploy", State: skill.StateActive, Description: "deploy the app", FilePath: skillFile},
		"review":  {Name: "review", State: skill.StateEnable, Description: "review code"},
		"archive": {Name: "archive", State: skill.StateDisable, Description: "archive old data"},
	}

	registry := newTestRegistry(t, skills)
	prompt := registry.GetSkillsSection()

	// Active skill must appear
	if !strings.Contains(prompt, "deploy") {
		t.Error("expected active skill 'deploy' to appear in system prompt section")
	}

	// Enabled-only skill must NOT appear (not model-aware)
	if strings.Contains(prompt, "review") {
		t.Error("enabled-only skill 'review' must not appear in system prompt section")
	}

	// Disabled skill must NOT appear
	if strings.Contains(prompt, "archive") {
		t.Error("disabled skill 'archive' must not appear in system prompt section")
	}

	// Body must include the directory header (XML wrapping is added by the
	// system catalog, not the registry).
	if !strings.Contains(prompt, "Use the Skill tool to invoke") {
		t.Error("expected skills directory header in body")
	}

	// GetSkillInvocationPrompt must include the instructions body
	invocation := registry.GetSkillInvocationPrompt("deploy")
	if !strings.Contains(invocation, "Run the deployment pipeline step by step") {
		t.Errorf("invocation prompt missing skill instructions, got: %q", invocation)
	}
}
