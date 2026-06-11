package system

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
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

func TestBuildPromptOmitsMemory(t *testing.T) {
	// Memory (CLAUDE.md / SAN.md) no longer lives in the system prompt — it
	// rides on user messages as a <system-reminder> block via the harness's
	// reminder service so memory edits don't invalidate the cache prefix.
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)

	prompt := sys.Prompt()
	if strings.Contains(prompt, "<memory") {
		t.Error("prompt should NOT contain memory section (now a system-reminder)")
	}
}

func TestBuildPromptOmitsCapabilities(t *testing.T) {
	// Skills and agents directories no longer live in the system prompt.
	// Skills ride on user messages as <system-reminder> blocks; agents are
	// embedded in the Agent tool's description.
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test"}),
	)

	prompt := sys.Prompt()
	if strings.Contains(prompt, "<skills>") {
		t.Error("prompt should NOT contain <skills> tag (now lives in <system-reminder>)")
	}
	if strings.Contains(prompt, "<agents>") {
		t.Error("prompt should NOT contain <agents> tag (now lives in Agent tool description)")
	}
}

func TestBuildPromptOrder_StableBeforeVolatile(t *testing.T) {
	// Volatile sections (environment) must sit AFTER stable ones so the
	// prompt-cache prefix survives daily date rollovers.
	sys := Build(core.ScopeMain,
		WithEnvironment(Environment{Cwd: "/tmp/test", IsGit: true}),
	)
	prompt := sys.Prompt()

	indices := map[string]int{
		"identity": strings.Index(prompt, "interactive AI assistant"),
		"behavior": strings.Index(prompt, "<behavior>"),
		"rules":    strings.Index(prompt, "<rules>"),
		"env":      strings.Index(prompt, "<environment>"),
	}
	for name, idx := range indices {
		if idx < 0 {
			t.Fatalf("section %q not found", name)
		}
	}

	order := []string{"identity", "behavior", "rules", "env"}
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

	if !strings.Contains(withGit.Prompt(), "## Git safety") {
		t.Error("git=true should include git safety rules")
	}
	if strings.Contains(withoutGit.Prompt(), "## Git safety") {
		t.Error("git=false should omit git safety rules")
	}
}

func TestSystemUseDropRefresh(t *testing.T) {
	sys := Build(core.ScopeMain, WithEnvironment(Environment{Cwd: "/tmp/test"}))
	first := sys.Prompt()

	// Use: register a new section.
	sys.Use(core.Section{
		Slot: core.SlotEnvironment, Name: "test-section", Source: core.Dynamic,
		Render: func() string { return "TEST_SECTION_BODY" },
	}, "test")
	if !strings.Contains(sys.Prompt(), "TEST_SECTION_BODY") {
		t.Error("Use should add a new section's content to Prompt()")
	}

	// Drop: remove it.
	sys.Drop("test-section", "test")
	if strings.Contains(sys.Prompt(), "TEST_SECTION_BODY") {
		t.Error("Drop should remove the section from Prompt()")
	}

	// After Drop the prompt should match the original.
	if sys.Prompt() != first {
		t.Error("Prompt should return to original state after Drop")
	}
}

func TestCachedTemplatesNonEmpty(t *testing.T) {
	for name, body := range map[string]string{
		"cachedIdentity":  cachedIdentity,
		"cachedBehavior":  cachedBehavior,
		"cachedRules":     cachedRules,
		"cachedRulesMain": cachedRulesMain,
		"cachedRulesGit":  cachedRulesGit,
		"cachedCompact":   cachedCompact,
	} {
		if body == "" {
			t.Errorf("%s should be non-empty after init()", name)
		}
	}
}

func TestCompactPrompt(t *testing.T) {
	if CompactPrompt() == "" {
		t.Error("CompactPrompt() should return non-empty string")
	}
}
