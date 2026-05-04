package system

import (
	"strings"
	"testing"

	"github.com/genai-io/gen-code/internal/core"
)

func TestBuildEnvironmentRendersFacts(t *testing.T) {
	body := renderEnvironment(Environment{Cwd: "/tmp/project", IsGit: true, ModelID: "test-model"})
	if !strings.Contains(body, "cwd: /tmp/project") {
		t.Fatalf("renderEnvironment missing cwd: %q", body)
	}
	if !strings.Contains(body, "git: yes") {
		t.Fatalf("renderEnvironment missing git status: %q", body)
	}
	if !strings.Contains(body, "model: test-model") {
		t.Fatalf("renderEnvironment missing model: %q", body)
	}
}

func TestBuildPromptCaching(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
	)

	first := sys.Prompt()
	if first == "" {
		t.Error("First Prompt() call should return non-empty string")
	}

	second := sys.Prompt()
	if first != second {
		t.Error("Second Prompt() call should return cached result identical to the first")
	}
}

func TestBuildPromptContainsMemory(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
		WithMemory("Always use tabs for indentation.", "This is a Go project using Bubble Tea."),
	)

	prompt := sys.Prompt()
	if !strings.Contains(prompt, `<memory scope="user">`) {
		t.Error("prompt should contain <memory scope=\"user\"> tag")
	}
	if !strings.Contains(prompt, "Always use tabs for indentation.") {
		t.Error("prompt should contain user memory content")
	}
	if !strings.Contains(prompt, `<memory scope="project">`) {
		t.Error("prompt should contain <memory scope=\"project\"> tag")
	}
	if !strings.Contains(prompt, "This is a Go project using Bubble Tea.") {
		t.Error("prompt should contain project memory content")
	}
}

func TestBuildPromptContainsCapabilities(t *testing.T) {
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
		WithSkills("- commit: Write commit messages"),
		WithAgents("- explorer: read-only research"),
	)

	prompt := sys.Prompt()
	if !strings.Contains(prompt, "<skills>") {
		t.Error("prompt should contain <skills> tag")
	}
	if !strings.Contains(prompt, "<agents>") {
		t.Error("prompt should contain <agents> tag")
	}
}

func TestBuildPromptOrder_StableBeforeVolatile(t *testing.T) {
	// Volatile sections (env, notice) must sit AFTER stable ones so the
	// prompt-cache prefix survives daily date rollovers and hook injections.
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
		WithMemory("USER_MARKER", "PROJECT_MARKER"),
		WithSkills("SKILLS_MARKER"),
		WithAgents("AGENTS_MARKER"),
		WithNotice("test", "NOTICE_MARKER"),
	)
	prompt := sys.Prompt()

	indices := map[string]int{
		"identity":   strings.Index(prompt, "interactive AI assistant"),
		"policy":     strings.Index(prompt, "<policy>"),
		"guidelines": strings.Index(prompt, `<guidelines name="tools">`),
		// guidelines body markers come from the new tools.txt which no longer has a # header

		"user":    strings.Index(prompt, "USER_MARKER"),
		"project": strings.Index(prompt, "PROJECT_MARKER"),
		"skills":  strings.Index(prompt, "SKILLS_MARKER"),
		"agents":  strings.Index(prompt, "AGENTS_MARKER"),
		"env":     strings.Index(prompt, "<environment>"),
		"notice":  strings.Index(prompt, "NOTICE_MARKER"),
	}
	for name, idx := range indices {
		if idx < 0 {
			t.Fatalf("section %q not found", name)
		}
	}

	order := []string{"identity", "policy", "guidelines", "user", "project", "skills", "agents", "env", "notice"}
	for i := 1; i < len(order); i++ {
		if indices[order[i-1]] >= indices[order[i]] {
			t.Errorf("expected %s before %s; got idx %d vs %d",
				order[i-1], order[i], indices[order[i-1]], indices[order[i]])
		}
	}
}

func TestBuildPromptEmptyOptionsExcluded(t *testing.T) {
	sys := Build(core.ScopeMain, WithEnvironment(Environment{Cwd: "/tmp/test"}))
	prompt := sys.Prompt()

	if strings.Contains(prompt, "<memory") {
		t.Error("empty memory should not produce <memory> tag")
	}
	if strings.Contains(prompt, "<skills>") {
		t.Error("empty skills should not produce <skills> tag")
	}
	if strings.Contains(prompt, "<agents>") {
		t.Error("empty agents should not produce <agents> tag")
	}
}

func TestBuildScopeMain_HasTaskAndQuestionGuidelines(t *testing.T) {
	sys := Build(core.ScopeMain, WithEnvironment(Environment{Cwd: "/tmp/test"}))
	prompt := sys.Prompt()

	if !strings.Contains(prompt, "TaskCreate") {
		t.Error("main scope should include task guidelines")
	}
	if !strings.Contains(prompt, "AskUserQuestion") {
		t.Error("main scope should include question guidelines")
	}
}

func TestBuildScopeSubagent_OmitsMainOnlyGuidelines(t *testing.T) {
	sys := Build(core.ScopeSubagent, WithEnvironment(Environment{Cwd: "/tmp/test"}))
	prompt := sys.Prompt()

	if strings.Contains(prompt, "TaskCreate") {
		t.Error("subagent scope should not include task guidelines")
	}
	if strings.Contains(prompt, "AskUserQuestion") {
		t.Error("subagent scope should not include question guidelines")
	}
}

func TestBuildSubagentIdentity_ReplacesDefault(t *testing.T) {
	sys := Build(core.ScopeSubagent,
		WithSubagentIdentity(SubagentBrief{
			AgentName:    "code-reviewer",
			Description:  "Reviews code changes for bugs.",
			Mode:         "explore",
			CustomPrompt: "Use git diff to inspect changes.",
		}),
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)
	prompt := sys.Prompt()

	if !strings.Contains(prompt, "You are a code-reviewer subagent") {
		t.Error("subagent identity should announce agent name")
	}
	if !strings.Contains(prompt, `<identity mode="explore">`) {
		t.Error("identity tag should carry mode attribute")
	}
	if !strings.Contains(prompt, "Use git diff to inspect changes.") {
		t.Error("custom prompt body should appear inside identity")
	}
	// Default identity should be replaced, not duplicated.
	if strings.Contains(prompt, "interactive AI assistant") {
		t.Error("default identity should be replaced by subagent identity")
	}
}

func TestBuildGitGuidelinesToggle(t *testing.T) {
	withGit := Build(core.ScopeMain,
		WithGitGuidelines(true),
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
	)
	withoutGit := Build(core.ScopeMain,
		WithGitGuidelines(false),
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: false}),
	)

	if !strings.Contains(withGit.Prompt(), `<guidelines name="git">`) {
		t.Error("git=true should include git guidelines")
	}
	if strings.Contains(withoutGit.Prompt(), `<guidelines name="git">`) {
		t.Error("git=false should omit git guidelines")
	}
}

func TestSystemUseDropRefresh(t *testing.T) {
	sys := Build(core.ScopeMain, WithEnvironment(Environment{Cwd: "/tmp/test"}))
	first := sys.Prompt()

	// Use: register a new section.
	sys.Use(core.Section{
		Slot: core.SlotInvocation, Name: "invocation-test", Source: core.Dynamic,
		Render: func() string { return "INVOCATION_BODY" },
	})
	if !strings.Contains(sys.Prompt(), "INVOCATION_BODY") {
		t.Error("Use should add a new section's content to Prompt()")
	}

	// Drop: remove it.
	sys.Drop("invocation-test")
	if strings.Contains(sys.Prompt(), "INVOCATION_BODY") {
		t.Error("Drop should remove the section from Prompt()")
	}

	// After Drop the prompt should match the original.
	if sys.Prompt() != first {
		t.Error("Prompt should return to original state after Drop")
	}
}

func TestCachedTemplatesNonEmpty(t *testing.T) {
	if cachedIdentity == "" {
		t.Error("cachedIdentity should be non-empty after init()")
	}
	if cachedPolicy == "" {
		t.Error("cachedPolicy should be non-empty after init()")
	}
	if cachedTools == "" {
		t.Error("cachedTools should be non-empty after init()")
	}
}

func TestCompactPrompt(t *testing.T) {
	if CompactPrompt() == "" {
		t.Error("CompactPrompt() should return non-empty string")
	}
}
