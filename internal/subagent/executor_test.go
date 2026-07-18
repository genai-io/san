package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/tool"
)

// llm.ParseVendorModel gates "vendor/model" routing on registered providers, so
// the tests that exercise routing register the vendors they reference. (The app
// wires these via blank imports in cmd/san/main.go.)
func init() {
	llm.RegisterProviderDisplay(llm.DeepSeek, llm.ProviderDisplay{Name: "DeepSeek"})
	llm.RegisterProviderDisplay(llm.Anthropic, llm.ProviderDisplay{Name: "Anthropic"})
}

type stubSubagentSessionStore struct {
	saveParentID string
	saveTitle    string
	saveModelID  string
	saveCwd      string
	saveMessages []core.Message
	loadMessages []core.Message
	loadErr      error
}

func (s *stubSubagentSessionStore) SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error) {
	s.saveParentID = parentSessionID
	s.saveTitle = title
	s.saveModelID = modelID
	s.saveCwd = cwd
	s.saveMessages = append([]core.Message(nil), messages...)
	return "agent-1", "/tmp/transcripts/agent-1.jsonl", nil
}

func (s *stubSubagentSessionStore) LoadSubagentMessages(agentID string) ([]core.Message, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return append([]core.Message(nil), s.loadMessages...), nil
}

func TestPrepareRunConfigRespectsOverrides(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}

	rc, err := executor.prepareRunConfig(context.Background(), tool.AgentExecRequest{
		Agent:    "general-purpose",
		Name:     "Scout",
		Model:    "override-model",
		MaxSteps: 120,
		Mode:     string(PermissionAcceptEdits),
	})
	if err != nil {
		t.Fatalf("prepareRunConfig() error: %v", err)
	}

	if rc.displayName != "Scout" {
		t.Fatalf("expected display name override, got %q", rc.displayName)
	}
	if rc.modelID != "override-model" {
		t.Fatalf("expected model override, got %q", rc.modelID)
	}
	if rc.maxSteps != 120 {
		t.Fatalf("expected max steps override, got %d", rc.maxSteps)
	}
	if rc.permMode != PermissionAcceptEdits {
		t.Fatalf("expected permission mode override, got %q", rc.permMode)
	}
	if rc.brief.Mode != string(PermissionAcceptEdits) {
		t.Fatalf("expected accept-edits mode in brief, got %q", rc.brief.Mode)
	}
}

func TestPrepareRunConfigDoesNotLowerBuiltinMaxSteps(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}

	rc, err := executor.prepareRunConfig(context.Background(), tool.AgentExecRequest{
		Agent:    "general-purpose",
		MaxSteps: 20,
	})
	if err != nil {
		t.Fatalf("prepareRunConfig() error: %v", err)
	}

	if rc.maxSteps != defaultMaxSteps {
		t.Fatalf("expected low max steps override to be raised to %d, got %d", defaultMaxSteps, rc.maxSteps)
	}
}

func TestResolveModelUsesConfigBeforeParent(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}
	ctx := context.Background()

	if _, got, onParent, _ := executor.resolveModel(ctx, "", "sonnet"); got != "claude-sonnet-4-20250514" || !onParent {
		t.Fatalf("config alias = %q onParent=%v, want sonnet alias on parent provider", got, onParent)
	}
	if _, got, onParent, _ := executor.resolveModel(ctx, "", "inherit"); got != "parent-model" || !onParent {
		t.Fatalf("inherit model = %q onParent=%v, want parent on parent provider", got, onParent)
	}
	if _, got, _, _ := executor.resolveModel(ctx, "override-model", "sonnet"); got != "override-model" {
		t.Fatalf("request override = %q, want override", got)
	}
}

// stubResolver records the vendor it was asked to resolve.
type stubResolver struct {
	provider llm.Provider
	vendor   llm.Name
	err      error
}

func (s *stubResolver) Resolve(_ context.Context, p llm.Name) (llm.Provider, error) {
	s.vendor = p
	return s.provider, s.err
}

func TestResolveModelRoutesQualifiedRefToResolver(t *testing.T) {
	stub := &stubResolver{}
	executor := &Executor{parentModelID: "parent-model", resolver: stub}

	_, modelID, onParent, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4", "")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if stub.vendor != llm.DeepSeek {
		t.Fatalf("resolver vendor = %q, want %q", stub.vendor, llm.DeepSeek)
	}
	if modelID != "deepseek-v4" {
		t.Fatalf("modelID = %q, want deepseek-v4", modelID)
	}
	if onParent {
		t.Fatal("a routed vendor/model must not be flagged as on the parent provider")
	}
}

func TestResolveModelQualifiedRefWithoutResolverErrors(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"} // no resolver wired

	if _, _, _, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4", ""); err == nil {
		t.Fatal("expected an error when an explicit vendor/model ref has no resolver")
	}
}

func TestResolveModelPropagatesResolverError(t *testing.T) {
	stub := &stubResolver{err: errors.New("provider \"deepseek\" is not connected")}
	executor := &Executor{parentModelID: "parent-model", resolver: stub}

	if _, _, _, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4", ""); err == nil {
		t.Fatal("expected the resolver error to propagate")
	}
}

func TestParseVendorModel(t *testing.T) {
	tests := []struct {
		ref    string
		vendor llm.Name
		model  string
		ok     bool
	}{
		{"deepseek/deepseek-v4", llm.DeepSeek, "deepseek-v4", true},
		{"anthropic/claude-opus-4-20250514", llm.Anthropic, "claude-opus-4-20250514", true},
		{"acme/some-model", "", "", false},        // unknown vendor -> treated as a bare model id
		{"xiaomi/mimo-v2-flash", "", "", false},   // mimo ships slash ids; "xiaomi" is not a vendor name
		{"opus", "", "", false},                   // alias, not a qualified ref
		{"claude-opus-4-20250514", "", "", false}, // bare model id, no slash
		{"deepseek/", "", "", false},              // empty model
		{"/deepseek-v4", "", "", false},           // empty vendor
	}
	for _, tt := range tests {
		vendor, model, ok := llm.ParseVendorModel(tt.ref)
		if ok != tt.ok || vendor != tt.vendor || model != tt.model {
			t.Fatalf("ParseVendorModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.ref, vendor, model, ok, tt.vendor, tt.model, tt.ok)
		}
	}
}

func TestShouldRetryWithParentModelOnlyForMissingDifferentModel(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		modelID     string
		parentModel string
		want        bool
	}{
		{name: "openai model not found", err: errors.New(`infer: POST "https://api.openai.com/v1/responses": 400 Bad Request {"code":"model_not_found"}`), modelID: "claude-sonnet-4-20250514", parentModel: "gpt-5.5", want: true},
		{name: "same model", err: errors.New("model_not_found"), modelID: "gpt-5.5", parentModel: "gpt-5.5", want: false},
		{name: "no parent", err: errors.New("model_not_found"), modelID: "missing-model", parentModel: "", want: false},
		{name: "other error", err: errors.New("authentication failed"), modelID: "missing-model", parentModel: "gpt-5.5", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetryWithParentModel(tt.err, tt.modelID, tt.parentModel); got != tt.want {
				t.Fatalf("shouldRetryWithParentModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildCancelledAgentResultUsesPreparedRunMetadata(t *testing.T) {
	executor := &Executor{}
	run := &preparedRun{
		req: tool.AgentExecRequest{Agent: "general-purpose"},
		cfg: &runConfig{
			displayName: "Scout",
			modelID:     "test-model",
		},
		startedAt: time.Now().Add(-time.Second),
		activity:  []string{"Read(main.go)"},
	}

	result := executor.buildCancelledAgentResult(run, &core.Result{
		Content:    "partial",
		Messages:   []core.Message{{Role: core.RoleAssistant, Content: "partial"}},
		Steps:      2,
		ToolUses:   1,
		StopReason: core.StopCancelled,
	})
	if result == nil {
		t.Fatal("expected cancelled result")
	}
	if result.AgentName != "Scout" {
		t.Fatalf("expected prepared display name, got %q", result.AgentName)
	}
	if result.Model != "test-model" {
		t.Fatalf("expected prepared model, got %q", result.Model)
	}
	if len(result.Activity) != 1 || result.Activity[0] != "Read(main.go)" {
		t.Fatalf("unexpected activity: %#v", result.Activity)
	}
	if result.Error != "agent cancelled" {
		t.Fatalf("unexpected error: %q", result.Error)
	}
}

func TestFormatToolActivityUsesReadableAgentLabel(t *testing.T) {
	got := formatToolActivity("Agent", map[string]any{
		"subagent_type": "code-reviewer",
		"description":   "HA code structure",
		"prompt":        "Inspect the codebase",
	})

	if got != "Agent - Code Reviewer: HA code structure" {
		t.Fatalf("formatToolActivity() = %q, want %q", got, "Agent - Code Reviewer: HA code structure")
	}
}

func TestFormatToolActivityUsesShortGeneralName(t *testing.T) {
	got := formatToolActivity("Agent", map[string]any{
		"subagent_type": "general-purpose",
		"description":   "update repo references",
	})

	if got != "Agent - General: update repo references" {
		t.Fatalf("formatToolActivity() = %q, want %q", got, "Agent - General: update repo references")
	}
}

func TestFormatToolActivityNamesGeneralAgentByMode(t *testing.T) {
	for _, tc := range []struct {
		agent string
		mode  string
		desc  string
		want  string
	}{
		{agent: "general-purpose", mode: "explore", desc: "inspect repo", want: "Agent - Explorer: inspect repo"},
		{agent: "general-purpose", mode: "acceptEdits", desc: "update files", want: "Agent - Editor: update files"},
		{agent: "explorer", mode: "acceptEdits", desc: "update files", want: "Agent - Editor: update files"},
	} {
		got := formatToolActivity("Agent", map[string]any{
			"subagent_type": tc.agent,
			"description":   tc.desc,
			"mode":          tc.mode,
		})
		if got != tc.want {
			t.Fatalf("formatToolActivity(mode=%s) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestFormatToolActivityFallsBackToTaskOutputID(t *testing.T) {
	got := formatToolActivity("TaskOutput", map[string]any{
		"task_id": "task-123",
	})

	if got != "TaskOutput(task-123)" {
		t.Fatalf("formatToolActivity() = %q, want %q", got, "TaskOutput(task-123)")
	}
}

func TestBuildSystemPrompt_IncludesAdditionalInstructionsAndPreloadedSkills(t *testing.T) {
	prev := skill.DefaultIfInit()
	t.Cleanup(func() { skill.SetDefaultRegistry(prev) })

	tmpDir := t.TempDir()
	skillFile := filepath.Join(tmpDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(`---
name: commit
description: Write commit messages
---
Use conventional commits.
`), 0o644); err != nil {
		t.Fatalf("WriteFile(skill): %v", err)
	}

	userStore, err := skill.NewStore(filepath.Join(tmpDir, "user-skills.json"))
	if err != nil {
		t.Fatalf("NewStore(user): %v", err)
	}
	projectStore, err := skill.NewStore(filepath.Join(tmpDir, "project-skills.json"))
	if err != nil {
		t.Fatalf("NewStore(project): %v", err)
	}

	skill.SetDefaultRegistry(skill.NewRegistryForTest(map[string]*skill.Skill{
		"git:commit": {
			Name:      "commit",
			Namespace: "git",
			FilePath:  skillFile,
			SkillDir:  tmpDir,
			State:     skill.StateActive,
		},
	}, userStore, projectStore))

	executor := &Executor{}
	brief := executor.buildBrief(&AgentConfig{
		Name:         "Reviewer",
		Description:  "Reviews code changes.",
		SystemPrompt: "Prefer minimal, surgical fixes.",
		Skills:       []string{"git:commit"},
	}, PermissionDefault)

	if !strings.Contains(brief.CustomPrompt, "Prefer minimal, surgical fixes.") {
		t.Fatal("expected custom system prompt content in brief")
	}
	if !strings.Contains(brief.CustomPrompt, `<skill-invocation name="git:commit">`) {
		t.Fatal("expected preloaded skill invocation block in brief")
	}
	if !strings.Contains(brief.CustomPrompt, "Use conventional commits.") {
		t.Fatal("expected skill instructions in brief")
	}
}

func TestCapabilityPromptsFollowReachableTools(t *testing.T) {
	executor := &Executor{
		skillsPrompt: "- review: Review changes",
		agentsPrompt: "- code-reviewer: Review code",
	}

	directoryBody := func(getter func() string) string {
		if getter == nil {
			return ""
		}
		return getter()
	}

	skills, agentDir := executor.capabilityPrompts(&AgentConfig{AllowTools: nil})
	if skills == "" || directoryBody(agentDir) == "" {
		t.Fatalf("nil AllowTools should expose all capability prompts, got skills=%q agents=%q", skills, directoryBody(agentDir))
	}

	skills, agentDir = executor.capabilityPrompts(&AgentConfig{AllowTools: ToolNames("Read", "Skill")})
	if skills == "" || directoryBody(agentDir) != "" {
		t.Fatalf("Skill-only agent should expose skills but not agents, got skills=%q agents=%q", skills, directoryBody(agentDir))
	}

	skills, agentDir = executor.capabilityPrompts(&AgentConfig{AllowTools: ToolNames("Read", "Agent")})
	if skills != "" || directoryBody(agentDir) == "" {
		t.Fatalf("Agent-capable agent should expose agents but not skills, got skills=%q agents=%q", skills, directoryBody(agentDir))
	}

	skills, agentDir = executor.capabilityPrompts(&AgentConfig{AllowTools: ToolNames("Read")})
	if skills != "" || directoryBody(agentDir) != "" {
		t.Fatalf("read-only agent should not expose capability prompts, got skills=%q agents=%q", skills, directoryBody(agentDir))
	}
}

func TestExploreModeFiltersMutatingToolSchemas(t *testing.T) {
	schemas := []core.ToolSchema{
		{Name: "Read"},
		{Name: "Grep"},
		{Name: "Write"},
		{Name: "Bash"},
		{Name: "WebSearch"},
	}

	allowedBash := ToolList{{Name: "Bash", Pattern: "git diff*"}}

	got := filterSchemasForPermission(schemas, PermissionExplore, allowedBash)
	want := []core.ToolSchema{{Name: "Bash"}}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered schemas = %+v, want %+v", got, want)
	}

	got = filterSchemasForPermission(schemas, PermissionExplore, nil)
	want = []core.ToolSchema{{Name: "Read"}, {Name: "Grep"}, {Name: "WebSearch"}}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered schemas without git diff = %+v, want %+v", got, want)
	}
}

func TestExploreModeAllowsOnlyGitDiffBash(t *testing.T) {
	check := subagentPermissionFunc(PermissionExplore, ToolList{{Name: "Bash", Pattern: "git diff*"}}, nil)
	for _, command := range []string{
		"git diff",
		"git diff --stat",
		"git diff --cached -- internal/subagent/executor.go",
	} {
		allow, reason := check(context.Background(), "Bash", map[string]any{"command": command})
		if !allow {
			t.Fatalf("Bash(%q) blocked: %s", command, reason)
		}
	}

	// Cases that should be blocked: pattern mismatch, or bypass-immune
	// destructive subcommand. The pattern Bash(git diff*) is greedy so
	// "git diff > /tmp/foo" naturally matches — users wanting tighter
	// scope should write a more specific pattern.
	blocked := []string{
		"git status",                          // pattern mismatch
		"git diff && rm -rf /tmp/example",     // bypass-immune destructive
		"git diff && git push --force origin", // bypass-immune destructive
	}
	for _, command := range blocked {
		allow, _ := check(context.Background(), "Bash", map[string]any{"command": command})
		if allow {
			t.Fatalf("Bash(%q) allowed, want blocked", command)
		}
	}

	allow, _ := subagentPermissionFunc(PermissionExplore, nil, nil)(context.Background(), "Bash", map[string]any{"command": "git diff"})
	if allow {
		t.Fatal("git diff allowed without agent permission")
	}
}

func TestDefaultModeRestrictsConfiguredBash(t *testing.T) {
	check := subagentPermissionFunc(PermissionDefault, ToolList{{Name: "Bash", Pattern: "git diff*"}}, nil)
	allow, reason := check(context.Background(), "Bash", map[string]any{"command": "git diff --stat"})
	if !allow {
		t.Fatalf("configured Bash command blocked: %s", reason)
	}

	allow, _ = check(context.Background(), "Bash", map[string]any{"command": "git status"})
	if allow {
		t.Fatal("unconfigured Bash command allowed (allow_tools whitelist constraint)")
	}

	allow, reason = check(context.Background(), "Read", map[string]any{"file_path": "README.md"})
	if !allow {
		t.Fatalf("non-Bash default mode tool blocked: %s", reason)
	}
}

func TestDenyToolRulesMatchPatterns(t *testing.T) {
	check := subagentPermissionFunc(PermissionDefault, nil, ToolList{{Name: "Bash", Pattern: "git status"}})
	allow, _ := check(context.Background(), "Bash", map[string]any{"command": "git status"})
	if allow {
		t.Fatal("denied Bash command allowed")
	}

	allow, reason := check(context.Background(), "Bash", map[string]any{"command": "git diff --stat"})
	// Default mode + no allow_tools -> Bash would Prompt -> Deny in subagent.
	if allow {
		t.Fatalf("default-mode Bash unexpectedly allowed without allow_tools: %s", reason)
	}
}

func TestExploreModeAllowsConfiguredBashPattern(t *testing.T) {
	check := subagentPermissionFunc(PermissionExplore, ToolList{{Name: "Bash", Pattern: "git show*"}}, nil)
	allow, reason := check(context.Background(), "Bash", map[string]any{"command": "git show --stat HEAD"})
	if !allow {
		t.Fatalf("configured bash command blocked: %s", reason)
	}

	allow, _ = check(context.Background(), "Bash", map[string]any{"command": "git diff --stat"})
	if allow {
		t.Fatal("unconfigured bash command allowed")
	}
}

func TestAcceptEditsModeFiltersApprovalOnlyToolSchemas(t *testing.T) {
	schemas := []core.ToolSchema{
		{Name: "Read"},
		{Name: "Edit"},
		{Name: "Write"},
		{Name: "Bash"},
		{Name: "Agent"},
	}

	got := filterSchemasForPermission(schemas, PermissionAcceptEdits, nil)
	want := []core.ToolSchema{{Name: "Read"}, {Name: "Edit"}, {Name: "Write"}}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered schemas = %+v, want %+v", got, want)
	}
}

func TestBypassModeAllowsEverything(t *testing.T) {
	check := subagentPermissionFunc(PermissionBypass, nil, nil)
	allow, _ := check(context.Background(), "Bash", map[string]any{"command": "git status"})
	if !allow {
		t.Fatal("bypass mode should allow Bash")
	}
	allow, _ = check(context.Background(), "Agent", map[string]any{})
	if !allow {
		t.Fatal("bypass mode should allow Agent")
	}
}

func TestNormalizePermissionModeDefaultsEmpty(t *testing.T) {
	if got := NormalizePermissionMode(""); got != PermissionDefault {
		t.Fatalf("normalize(empty) = %q, want %q", got, PermissionDefault)
	}
	if got := NormalizePermissionMode("  explore  "); got != PermissionExplore {
		t.Fatalf("normalize(\"  explore  \") = %q, want %q", got, PermissionExplore)
	}
}

func TestBuiltinAgentsDefaultTo100Turns(t *testing.T) {
	for _, agentName := range []string{"general-purpose", "code-simplifier", "code-reviewer"} {
		t.Run(agentName, func(t *testing.T) {
			cfg, ok := defaultRegistry.Get(agentName)
			if !ok {
				t.Fatalf("agent %q not found", agentName)
			}
			if cfg.MaxSteps != defaultMaxSteps {
				t.Fatalf("expected %q max steps to default to %d, got %d", agentName, defaultMaxSteps, cfg.MaxSteps)
			}
		})
	}
}

func TestPersistSubagentSessionUsesSessionStore(t *testing.T) {
	store := &stubSubagentSessionStore{}
	executor := &Executor{
		cwd:             "/tmp/project",
		sessionStore:    store,
		parentSessionID: "parent-1",
	}

	sessionID, transcriptPath := executor.persistSubagentSession("General", "test-model", "Inspect code", []core.Message{
		{Role: core.RoleUser, Content: "hello"},
	})

	if sessionID != "agent-1" {
		t.Fatalf("sessionID = %q, want %q", sessionID, "agent-1")
	}
	if transcriptPath != "/tmp/transcripts/agent-1.jsonl" {
		t.Fatalf("transcriptPath = %q", transcriptPath)
	}
	if store.saveParentID != "parent-1" || store.saveTitle != "Inspect code" || store.saveModelID != "test-model" || store.saveCwd != "/tmp/project" {
		t.Fatalf("unexpected save args: %+v", store)
	}
	if len(store.saveMessages) != 1 || store.saveMessages[0].Content != "hello" {
		t.Fatalf("unexpected saved messages: %+v", store.saveMessages)
	}
}

func TestResumeFromSessionUsesSessionStore(t *testing.T) {
	store := &stubSubagentSessionStore{
		loadMessages: []core.Message{
			{Role: core.RoleUser, Content: "previous"},
			{Role: core.RoleAssistant, Content: "response"},
		},
	}
	executor := &Executor{sessionStore: store}

	// Create a minimal core.Agent for testing
	ag := core.NewAgent(core.Config{
		LLM:    &stubLLM{},
		System: &stubSystem{},
		Tools:  core.NewTools(),
	})
	ctx := context.Background()

	if err := executor.resumeFromSession(ag, ctx, "agent-1", "continue"); err != nil {
		t.Fatalf("resumeFromSession(): %v", err)
	}

	msgs := ag.Messages()
	if len(msgs) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(msgs))
	}
	if msgs[2].Role != core.RoleUser || msgs[2].Content != "continue" {
		t.Fatalf("unexpected continuation message: %+v", msgs[2])
	}
}

func TestResumeFromSessionRequiresSessionStore(t *testing.T) {
	executor := &Executor{}
	ag := core.NewAgent(core.Config{
		LLM:    &stubLLM{},
		System: &stubSystem{},
		Tools:  core.NewTools(),
	})
	err := executor.resumeFromSession(ag, context.Background(), "agent-1", "continue")
	if err == nil || !strings.Contains(err.Error(), "session store not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResumeFromSessionPropagatesLoadError(t *testing.T) {
	executor := &Executor{
		sessionStore: &stubSubagentSessionStore{loadErr: errors.New("boom")},
	}
	ag := core.NewAgent(core.Config{
		LLM:    &stubLLM{},
		System: &stubSystem{},
		Tools:  core.NewTools(),
	})
	err := executor.resumeFromSession(ag, context.Background(), "agent-1", "continue")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// stubLLM is a minimal core.LLM for tests that don't call inference.
type stubLLM struct{}

func (s *stubLLM) Infer(_ context.Context, _ core.InferRequest) (<-chan core.Chunk, error) {
	ch := make(chan core.Chunk)
	close(ch)
	return ch, nil
}
func (s *stubLLM) InputLimit() int { return 0 }

// stubSystem is a minimal core.System for tests.
type stubSystem struct{}

func (s *stubSystem) Prompt() string                        { return "" }
func (s *stubSystem) Use(_ core.Section, _ string)          {}
func (s *stubSystem) Drop(_, _ string)                      {}
func (s *stubSystem) Refresh(_, _ string)                   {}
func (s *stubSystem) Sections() []core.Section              { return nil }
func (s *stubSystem) SetObserver(_ func(core.SystemChange)) {}
