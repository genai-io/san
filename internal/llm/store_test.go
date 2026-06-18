package llm

import "testing"

func TestStore_PersistsConnectionsCurrentModelSearchProviderAndTokenLimits(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	if err := store.Connect(OpenAI, AuthAPIKey); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := store.SetCurrentModel("gpt-5", OpenAI, AuthAPIKey); err != nil {
		t.Fatalf("SetCurrentModel() error = %v", err)
	}
	if err := store.SetSearchProvider("brave"); err != nil {
		t.Fatalf("SetSearchProvider() error = %v", err)
	}
	if err := store.SetTokenLimit("gpt-5", 200000, 32000); err != nil {
		t.Fatalf("SetTokenLimit() error = %v", err)
	}

	reloaded, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore(reload) error = %v", err)
	}

	if !reloaded.IsConnected(OpenAI, AuthAPIKey) {
		t.Fatal("expected OpenAI API key connection to persist")
	}
	current := reloaded.GetCurrentModel()
	if current == nil || current.ModelID != "gpt-5" || current.Provider != OpenAI || current.AuthMethod != AuthAPIKey {
		t.Fatalf("unexpected current model after reload: %#v", current)
	}
	if reloaded.GetSearchProvider() != "brave" {
		t.Fatalf("search provider = %q, want %q", reloaded.GetSearchProvider(), "brave")
	}
	in, out, ok := reloaded.GetTokenLimit("gpt-5")
	if !ok || in != 200000 || out != 32000 {
		t.Fatalf("unexpected token limit after reload: in=%d out=%d ok=%v", in, out, ok)
	}
}

func TestStore_SetTokenLimitUpdatesCachedModelCopy(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	models := []ModelInfo{
		{ID: "gpt-5", Name: "GPT-5"},
		{ID: "gpt-5-mini", Name: "GPT-5 mini"},
	}
	if err := store.CacheModels(OpenAI, AuthAPIKey, models); err != nil {
		t.Fatalf("CacheModels() error = %v", err)
	}

	cachedBefore, ok := store.GetCachedModels(OpenAI, AuthAPIKey)
	if !ok {
		t.Fatal("expected cached models")
	}

	if err := store.SetTokenLimit("gpt-5", 256000, 64000); err != nil {
		t.Fatalf("SetTokenLimit() error = %v", err)
	}

	cachedAfter, ok := store.GetCachedModels(OpenAI, AuthAPIKey)
	if !ok {
		t.Fatal("expected cached models after override")
	}
	if cachedAfter[0].InputTokenLimit != 256000 || cachedAfter[0].OutputTokenLimit != 64000 {
		t.Fatalf("expected cached override applied, got %#v", cachedAfter[0])
	}
	if cachedAfter[1].InputTokenLimit != 0 || cachedAfter[1].OutputTokenLimit != 0 {
		t.Fatalf("expected unrelated model unchanged, got %#v", cachedAfter[1])
	}
	if cachedBefore[0].InputTokenLimit != 0 || cachedBefore[0].OutputTokenLimit != 0 {
		t.Fatalf("expected previously returned cached slice to remain unchanged, got %#v", cachedBefore[0])
	}
}

// When the same model ID is cached under multiple provider/auth keys — one with
// a real display name and another that only echoes the raw ID — the display
// name must be stable across renders. Returning the first map match would
// flicker because Go randomizes map iteration order; we must deterministically
// prefer the real display name.
func TestStore_CachedModelDisplayNamePrefersRealNameAndIsStable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// alibaba echoes the raw ID as the display name; deepseek has a real one.
	if err := store.CacheModels(Alibaba, AuthAPIKey, []ModelInfo{
		{ID: "deepseek-v4-pro", Name: "deepseek-v4-pro", DisplayName: "deepseek-v4-pro"},
	}); err != nil {
		t.Fatalf("CacheModels(alibaba) error = %v", err)
	}
	if err := store.CacheModels(DeepSeek, AuthAPIKey, []ModelInfo{
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", DisplayName: "DeepSeek V4 Pro"},
	}); err != nil {
		t.Fatalf("CacheModels(deepseek) error = %v", err)
	}

	// Call many times; with randomized map order an order-dependent
	// implementation would return both values across iterations.
	for i := range 100 {
		if got := store.CachedModelDisplayName("deepseek-v4-pro"); got != "DeepSeek V4 Pro" {
			t.Fatalf("CachedModelDisplayName() = %q, want %q (unstable/wrong on iteration %d)", got, "DeepSeek V4 Pro", i)
		}
	}

	// A model that only ever echoes its ID still falls back to that ID.
	if err := store.CacheModels(OpenAI, AuthAPIKey, []ModelInfo{
		{ID: "raw-only", Name: "raw-only", DisplayName: "raw-only"},
	}); err != nil {
		t.Fatalf("CacheModels(openai) error = %v", err)
	}
	if got := store.CachedModelDisplayName("raw-only"); got != "raw-only" {
		t.Fatalf("CachedModelDisplayName(raw-only) = %q, want %q", got, "raw-only")
	}

	// An uncached ID returns "".
	if got := store.CachedModelDisplayName("missing"); got != "" {
		t.Fatalf("CachedModelDisplayName(missing) = %q, want empty", got)
	}
}

// A model can be cached under several provider keys where only some report a
// context window: an aggregator echoes the ID with no limit, while the native
// provider knows the real one. CachedModelLimits must prefer the known window
// over the zero, deterministically, so the status bar never flickers to "--".
func TestStore_CachedModelLimitsPrefersKnownWindow(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	// alibaba echoes the model with no context window; deepseek knows the real one.
	if err := store.CacheModels(Alibaba, AuthAPIKey, []ModelInfo{
		{ID: "deepseek-v4-pro"},
	}); err != nil {
		t.Fatalf("CacheModels(alibaba) error = %v", err)
	}
	if err := store.CacheModels(DeepSeek, AuthAPIKey, []ModelInfo{
		{ID: "deepseek-v4-pro", InputTokenLimit: 1_000_000, OutputTokenLimit: 384_000},
	}); err != nil {
		t.Fatalf("CacheModels(deepseek) error = %v", err)
	}

	// Call many times; with randomized map order an order-dependent
	// implementation would return the zero limit on some iterations.
	for i := range 100 {
		in, out := store.CachedModelLimits("deepseek-v4-pro")
		if in != 1_000_000 || out != 384_000 {
			t.Fatalf("CachedModelLimits() = (%d, %d), want (1000000, 384000) on iteration %d", in, out, i)
		}
	}

	// An uncached ID returns zero limits.
	if in, out := store.CachedModelLimits("missing"); in != 0 || out != 0 {
		t.Fatalf("CachedModelLimits(missing) = (%d, %d), want (0, 0)", in, out)
	}
}
