package subagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
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
}

func (s *stubSubagentSessionStore) SaveSubagentConversation(parentSessionID, title, modelID, cwd string, messages []core.Message) (string, string, error) {
	s.saveParentID = parentSessionID
	s.saveTitle = title
	s.saveModelID = modelID
	s.saveCwd = cwd
	s.saveMessages = append([]core.Message(nil), messages...)
	return "agent-1", "/tmp/transcripts/agent-1.jsonl", nil
}

func TestPrepareRunConfigRespectsOverrides(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}

	rc, err := executor.prepareRunConfig(context.Background(), tool.AgentExecRequest{
		Name:     "Scout",
		Model:    "override-model",
		MaxSteps: 600,
		Mode:     "edit",
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
	if rc.maxSteps != 600 {
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
		MaxSteps: 20,
	})
	if err != nil {
		t.Fatalf("prepareRunConfig() error: %v", err)
	}

	if rc.maxSteps != defaultMaxSteps {
		t.Fatalf("expected low max steps override to be raised to %d, got %d", defaultMaxSteps, rc.maxSteps)
	}
}

func TestResolveModelUsesRequestOrParent(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"}
	ctx := context.Background()

	if _, got, _ := executor.resolveModel(ctx, ""); got != "parent-model" {
		t.Fatalf("empty request model = %q, want parent", got)
	}
	if _, got, _ := executor.resolveModel(ctx, "inherit"); got != "parent-model" {
		t.Fatalf("inherit model = %q, want parent", got)
	}
	if _, got, _ := executor.resolveModel(ctx, "override-model"); got != "override-model" {
		t.Fatalf("request override = %q, want override", got)
	}
}

type stubProvider struct{}

func (stubProvider) Stream(context.Context, llm.CompletionOptions) <-chan llm.StreamChunk { return nil }
func (stubProvider) ListModels(context.Context) ([]llm.ModelInfo, error)                  { return nil, nil }
func (stubProvider) Name() string                                                         { return "stub" }

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
	stub := &stubResolver{provider: stubProvider{}}
	executor := &Executor{parentModelID: "parent-model", resolver: stub}

	_, modelID, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if stub.vendor != llm.DeepSeek {
		t.Fatalf("resolver vendor = %q, want %q", stub.vendor, llm.DeepSeek)
	}
	if modelID != "deepseek-v4" {
		t.Fatalf("modelID = %q, want deepseek-v4", modelID)
	}
}

func TestResolveModelQualifiedRefWithoutResolverInheritsParent(t *testing.T) {
	executor := &Executor{parentModelID: "parent-model"} // no resolver wired

	provider, modelID, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if provider != executor.provider || modelID != executor.parentModelID {
		t.Fatalf("resolveModel() = (%v, %q), want parent (%v, %q)", provider, modelID, executor.provider, executor.parentModelID)
	}
}

func TestResolveModelResolverErrorInheritsParent(t *testing.T) {
	stub := &stubResolver{err: errors.New("provider \"deepseek\" is not connected")}
	executor := &Executor{parentModelID: "parent-model", resolver: stub}

	provider, modelID, err := executor.resolveModel(context.Background(), "deepseek/deepseek-v4")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if provider != executor.provider || modelID != executor.parentModelID {
		t.Fatalf("resolveModel() = (%v, %q), want parent (%v, %q)", provider, modelID, executor.provider, executor.parentModelID)
	}
}

func newModelStore(t *testing.T, models []llm.ModelInfo) *llm.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("llm.NewStore() error: %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthSubscription, models); err != nil {
		t.Fatalf("CacheModels() error: %v", err)
	}
	return store
}

func TestResolveModelUnavailableOverrideInheritsParent(t *testing.T) {
	executor := &Executor{
		provider:           stubProvider{},
		modelStore:         newModelStore(t, []llm.ModelInfo{{ID: "gpt-5.6-sol"}}),
		parentProviderName: llm.OpenAI,
		parentAuthMethod:   llm.AuthSubscription,
		parentModelID:      "gpt-5.6-sol",
	}

	_, modelID, err := executor.resolveModel(context.Background(), "haiku")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if modelID != executor.parentModelID {
		t.Fatalf("resolveModel(haiku) model = %q, want parent %q", modelID, executor.parentModelID)
	}
}

func TestResolveModelUnavailableParentProviderQualifiedOverrideInheritsParent(t *testing.T) {
	executor := &Executor{
		provider:           stubProvider{},
		resolver:           &stubResolver{provider: stubProvider{}},
		modelStore:         newModelStore(t, []llm.ModelInfo{{ID: "gpt-5.6-sol"}}),
		parentProviderName: llm.OpenAI,
		parentAuthMethod:   llm.AuthSubscription,
		parentModelID:      "gpt-5.6-sol",
	}

	_, modelID, err := executor.resolveModel(context.Background(), "openai/nonexistent-model")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if modelID != executor.parentModelID {
		t.Fatalf("resolveModel(openai/nonexistent-model) model = %q, want parent %q", modelID, executor.parentModelID)
	}
}

func TestResolveModelEmptyCachedCatalogInheritsParent(t *testing.T) {
	executor := &Executor{
		provider:           stubProvider{},
		modelStore:         newModelStore(t, nil),
		parentProviderName: llm.OpenAI,
		parentAuthMethod:   llm.AuthSubscription,
		parentModelID:      "gpt-5.6-sol",
	}

	_, modelID, err := executor.resolveModel(context.Background(), "haiku")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if modelID != executor.parentModelID {
		t.Fatalf("resolveModel(haiku) model = %q, want parent %q", modelID, executor.parentModelID)
	}
}

func TestResolveModelAvailableOverrideIsPreserved(t *testing.T) {
	executor := &Executor{
		provider: stubProvider{},
		modelStore: newModelStore(t, []llm.ModelInfo{
			{ID: "gpt-5.6-sol"},
			{ID: "gpt-5.6-terra"},
		}),
		parentProviderName: llm.OpenAI,
		parentAuthMethod:   llm.AuthSubscription,
		parentModelID:      "gpt-5.6-sol",
	}

	_, modelID, err := executor.resolveModel(context.Background(), "gpt-5.6-terra")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if modelID != "gpt-5.6-terra" {
		t.Fatalf("resolveModel() model = %q, want gpt-5.6-terra", modelID)
	}
}

func TestResolveModelMissingCatalogLeavesOverrideUnverified(t *testing.T) {
	executor := &Executor{provider: stubProvider{}, parentModelID: "gpt-5.6-sol"}

	_, modelID, err := executor.resolveModel(context.Background(), "haiku")
	if err != nil {
		t.Fatalf("resolveModel() error: %v", err)
	}
	if modelID != "claude-haiku-4-5" {
		t.Fatalf("resolveModel() model = %q, want unresolved override to pass through", modelID)
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

func TestBuildUnfinishedAgentResultUsesPreparedRunMetadata(t *testing.T) {
	executor := &Executor{}
	run := &preparedRun{
		req: tool.AgentExecRequest{},
		cfg: &runConfig{
			displayName: "Scout",
			modelID:     "test-model",
		},
		startedAt: time.Now().Add(-time.Second),
		activity:  []string{"Read(main.go)"},
	}

	result := executor.buildUnfinishedAgentResult(run, &core.Result{
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

func TestFormatToolActivityUsesDefaultAgentLabel(t *testing.T) {
	got := formatToolActivity("Agent", map[string]any{
		"description": "update repo references",
	})

	if got != "General: update repo references" {
		t.Fatalf("formatToolActivity() = %q, want %q", got, "General: update repo references")
	}
}

func TestFormatToolActivityNamesDefaultAgentByMode(t *testing.T) {
	for _, tc := range []struct {
		mode string
		desc string
		want string
	}{
		{mode: "explore", desc: "inspect repo", want: "Explorer: inspect repo"},
		{mode: "edit", desc: "update files", want: "Editor: update files"},
	} {
		got := formatToolActivity("Agent", map[string]any{
			"description": tc.desc,
			"mode":        tc.mode,
		})
		if got != tc.want {
			t.Fatalf("formatToolActivity(mode=%s) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestFormatToolActivityFallsBackToEmptyParensForUnmappedTool(t *testing.T) {
	got := formatToolActivity("CustomTool", map[string]any{
		"task_id": "task-123",
	})

	if got != "CustomTool()" {
		t.Fatalf("formatToolActivity() = %q, want %q", got, "CustomTool()")
	}
}

func TestDefaultBriefIncludesIdentity(t *testing.T) {
	executor := &Executor{}
	brief := executor.buildBrief(PermissionDefault)

	if brief.AgentName != defaultAgentName || brief.Description != defaultAgentDescription {
		t.Fatalf("brief identity = %#v", brief)
	}
}

func TestExploreBriefDescribesEffectiveReadOnlyBashConstraint(t *testing.T) {
	executor := &Executor{}
	brief := executor.buildBrief(PermissionExplore)

	if !slices.Contains(brief.ToolConstraints, "Bash limited to commands classified as read-only") {
		t.Fatalf("tool constraints = %#v, want read-only Bash policy", brief.ToolConstraints)
	}
}

func TestSubagentRemindersFollowEffectiveMode(t *testing.T) {
	executor := &Executor{
		skillsPrompt:        "- review: Review changes",
		projectInstructions: "Follow project conventions.",
	}

	explore := strings.Join(executor.collectSubagentReminders(PermissionExplore), "\n")
	if !strings.Contains(explore, "Review changes") {
		t.Fatalf("explore reminders should include skills: %q", explore)
	}
	if strings.Contains(explore, "Follow project conventions") {
		t.Fatalf("explore reminders should omit edit instructions: %q", explore)
	}

	edit := strings.Join(executor.collectSubagentReminders(PermissionAcceptEdits), "\n")
	if !strings.Contains(edit, "Review changes") || !strings.Contains(edit, "Follow project conventions") {
		t.Fatalf("edit reminders should include skills and project instructions: %q", edit)
	}
}

func TestCanEditWorkspace(t *testing.T) {
	cases := []struct {
		name string
		mode PermissionMode
		want bool
	}{
		{"edit mode can edit", PermissionAcceptEdits, true},
		{"bypass mode can edit", PermissionBypass, true},
		{"explore mode is read-only", PermissionExplore, false},
		{"default mode cannot edit", PermissionDefault, false},
	}
	for _, tc := range cases {
		if got := canEditWorkspace(tc.mode); got != tc.want {
			t.Errorf("%s: canEditWorkspace(%q) = %v, want %v", tc.name, tc.mode, got, tc.want)
		}
	}
}

func TestExploreModeFiltersMutatingToolSchemas(t *testing.T) {
	schemas := []core.ToolSchema{
		{Name: "Read"},
		{Name: "Write"},
		{Name: "Bash"},
		{Name: "WebSearch"},
	}

	got := filterSchemasForPermission(schemas, PermissionExplore)
	want := []core.ToolSchema{{Name: "Read"}, {Name: "Bash"}, {Name: "WebSearch"}}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered schemas = %+v, want %+v", got, want)
	}
}

func TestExploreModeIsReadOnlyCeiling(t *testing.T) {
	check := subagentPermissionFunc(PermissionExplore)
	cases := []struct {
		name    string
		tool    string
		input   map[string]any
		allowed bool
	}{
		{name: "read", tool: "Read", input: map[string]any{"file_path": "README.md"}, allowed: true},
		{name: "write", tool: "Write", input: map[string]any{"file_path": "x", "content": "x"}},
		{name: "edit", tool: "Edit", input: map[string]any{"file_path": "x", "old_string": "a", "new_string": "b"}},
		{name: "mutating bash", tool: "Bash", input: map[string]any{"command": "touch x"}},
		{name: "git diff output", tool: "Bash", input: map[string]any{"command": "git diff --output=/tmp/x"}},
		{name: "git diff", tool: "Bash", input: map[string]any{"command": "git diff"}, allowed: true},
		{name: "git status", tool: "Bash", input: map[string]any{"command": "git status"}, allowed: true},
		{name: "git show", tool: "Bash", input: map[string]any{"command": "git show HEAD"}, allowed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := check(context.Background(), tc.tool, tc.input)
			if got != tc.allowed {
				t.Fatalf("allow = %v, want %v (reason=%q)", got, tc.allowed, reason)
			}
		})
	}
}

func TestDefaultModeUsesSharedPermissionDefaults(t *testing.T) {
	check := subagentPermissionFunc(PermissionDefault)
	allow, reason := check(context.Background(), "Read", map[string]any{"file_path": "README.md"})
	if !allow {
		t.Fatalf("Read blocked: %s", reason)
	}

	allow, reason = check(context.Background(), "Bash", map[string]any{"command": "git status"})
	if !allow {
		t.Fatalf("read-only Bash blocked: %s", reason)
	}

	allow, _ = check(context.Background(), "Bash", map[string]any{"command": "npm install"})
	if allow {
		t.Fatal("mutating Bash should collapse approval to deny")
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

	// Bash stays visible (read-only invocations auto-permit). Agent never
	// reaches this filter for workers — it is parent-only at the tool.Set
	// level.
	got := filterSchemasForPermission(schemas, PermissionAcceptEdits)
	want := []core.ToolSchema{{Name: "Read"}, {Name: "Edit"}, {Name: "Write"}, {Name: "Bash"}}
	if !slices.Equal(got, want) {
		t.Fatalf("filtered schemas = %+v, want %+v", got, want)
	}
}

func TestBypassModeAllowsEverything(t *testing.T) {
	check := subagentPermissionFunc(PermissionBypass)
	allow, _ := check(context.Background(), "Bash", map[string]any{"command": "git status"})
	if !allow {
		t.Fatal("bypass mode should allow Bash")
	}
	// Bypass skips both confirmation tiers. The circuit-breaker counterpart
	// (rm -rf ~ still denied) is pinned in TestPermissionScenarios.
	allow, reason := check(context.Background(), "Bash", map[string]any{"command": "git push --force origin main"})
	if !allow {
		t.Fatalf("bypass mode should allow work-discarding git: %s", reason)
	}
	allow, reason = check(context.Background(), "Bash", map[string]any{"command": "rm -rf /tmp/example"})
	if !allow {
		t.Fatalf("bypass mode should allow destructive bash on a subpath: %s", reason)
	}
	// Parent-only tools stay denied even in bypass mode — the agent model
	// is flat, and the gate backs up the schema-level exclusion.
	allow, _ = check(context.Background(), "Agent", map[string]any{})
	if allow {
		t.Fatal("parent-only Agent should be denied for workers even in bypass mode")
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

func TestRequestPermissionModeInheritanceAndCeiling(t *testing.T) {
	tests := []struct {
		name   string
		parent PermissionMode
		mode   string
		want   PermissionMode
	}{
		{name: "default inherits bypass", parent: PermissionBypass, mode: "default", want: PermissionBypass},
		{name: "empty inherits accept edits", parent: PermissionAcceptEdits, want: PermissionAcceptEdits},
		{name: "explore caps bypass", parent: PermissionBypass, mode: "explore", want: PermissionExplore},
		{name: "edit remains accept edits", parent: PermissionBypass, mode: "edit", want: PermissionAcceptEdits},
		{name: "headless safe default", parent: PermissionDefault, mode: "default", want: PermissionDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &Executor{parentPermissionMode: func() PermissionMode { return tt.parent }}
			if got := executor.requestPermissionMode(tool.AgentExecRequest{Mode: tt.mode}); got != tt.want {
				t.Fatalf("requestPermissionMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParentPermissionModeGetterUsesLiveSessionSnapshot(t *testing.T) {
	permissions := setting.NewSessionPermissions()
	executor := &Executor{}
	executor.SetParentPermissionMode(func() PermissionMode {
		return PermissionModeFromOperationMode(permissions.Snapshot().Mode)
	})

	if got := executor.requestPermissionMode(tool.AgentExecRequest{Mode: "default"}); got != PermissionDefault {
		t.Fatalf("initial inherited mode = %q, want default", got)
	}
	permissions.SetMode(setting.ModeBypassPermissions)
	if got := executor.requestPermissionMode(tool.AgentExecRequest{Mode: "default"}); got != PermissionBypass {
		t.Fatalf("updated inherited mode = %q, want bypass", got)
	}
	if got := executor.requestPermissionMode(tool.AgentExecRequest{Mode: "explore"}); got != PermissionExplore {
		t.Fatalf("explicit explore mode = %q, want read-only ceiling", got)
	}
}

func TestValidateRequestRejectsInjectedBypassMode(t *testing.T) {
	executor := &Executor{}
	for _, mode := range []string{"bypass", "bypassPermissions", "acceptEdits", "readonly"} {
		if err := executor.validateRequest(tool.AgentExecRequest{Prompt: "work", Mode: mode}); err == nil {
			t.Fatalf("validateRequest(mode=%q) unexpectedly allowed non-schema mode", mode)
		}
	}
}

func TestPermissionModeFromOperationMode(t *testing.T) {
	if got := PermissionModeFromOperationMode(setting.ModeBypassPermissions); got != PermissionBypass {
		t.Fatalf("bypass parent maps to %q", got)
	}
	if got := PermissionModeFromOperationMode(setting.ModeAutoPilot); got != PermissionAcceptEdits {
		t.Fatalf("autopilot parent maps to %q", got)
	}
}

func TestDefaultSubagentUses500Steps(t *testing.T) {
	if defaultMaxSteps != 500 {
		t.Fatalf("default subagent max steps = %d, want 500", defaultMaxSteps)
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

// TestBuildUnfinishedAgentResultPreservesFailedRun covers the other way a run
// ends early. A cancelled run was already preserved; a run that died on an
// inference failure was not, because ThinkAct returned no Result at all — so
// Run fell through to the bare-error path and persistSubagentSession never
// ran, losing the transcript of everything the agent had done.
func TestBuildUnfinishedAgentResultPreservesFailedRun(t *testing.T) {
	executor := &Executor{}
	run := &preparedRun{
		req:       tool.AgentExecRequest{},
		cfg:       &runConfig{displayName: "Scout", modelID: "test-model"},
		startedAt: time.Now().Add(-time.Second),
	}

	result := executor.buildUnfinishedAgentResult(run, &core.Result{
		Content:    "partial",
		Messages:   []core.Message{{Role: core.RoleAssistant, Content: "partial"}},
		Steps:      8,
		StopReason: core.StopError,
		StopDetail: "provider unavailable",
	})
	if result == nil {
		t.Fatal("a failed run must still be preserved, or its transcript is never persisted")
	}
	if result.Success {
		t.Error("Success = true, want false")
	}
	if result.Error != "provider unavailable" {
		t.Errorf("Error = %q, want the underlying failure", result.Error)
	}
	if result.Content != "partial" {
		t.Errorf("Content = %q, want the partial output", result.Content)
	}
	if result.StepCount != 8 {
		t.Errorf("StepCount = %d, want 8", result.StepCount)
	}
}

// A normally-completed run is not "unfinished" — it goes through
// buildAgentResult instead, so this guard must keep rejecting it.
func TestBuildUnfinishedAgentResultRejectsCompletedRun(t *testing.T) {
	executor := &Executor{}
	run := &preparedRun{
		req:       tool.AgentExecRequest{},
		cfg:       &runConfig{displayName: "Scout", modelID: "test-model"},
		startedAt: time.Now(),
	}

	if got := executor.buildUnfinishedAgentResult(run, &core.Result{StopReason: core.StopEndTurn}); got != nil {
		t.Fatalf("completed run treated as unfinished: %#v", got)
	}
	if got := executor.buildUnfinishedAgentResult(run, nil); got != nil {
		t.Fatalf("nil Result treated as unfinished: %#v", got)
	}
}
