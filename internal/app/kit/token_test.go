package kit

import (
	"testing"

	"github.com/genai-io/san/internal/llm"
)

// TestGetModelTokenLimitsPrefersCurrentProvider guards against the status-bar
// context window flickering when the same model ID is cached under multiple
// providers with different windows: it must resolve to the connected provider's
// value deterministically, not a random map hit.
func TestGetModelTokenLimitsPrefersCurrentProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := store.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{
		{ID: "gpt-5.5", ContextWindow: 400000, MaxOutput: 16384},
	}); err != nil {
		t.Fatalf("CacheModels(api_key): %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthSubscription, []llm.ModelInfo{
		{ID: "gpt-5.5", ContextWindow: 272000, MaxOutput: 16384},
	}); err != nil {
		t.Fatalf("CacheModels(subscription): %v", err)
	}

	current := &llm.CurrentModelInfo{
		ModelID:    "gpt-5.5",
		Provider:   llm.OpenAI,
		AuthMethod: llm.AuthSubscription,
	}

	// Repeat to catch the non-deterministic map iteration the bug relied on.
	for range 30 {
		if got := GetContextWindow(store, current); got != 272000 {
			t.Fatalf("input limit = %d, want 272000 (current provider's cache, not the 400k api_key entry)", got)
		}
	}
}

func TestGetModelTokenLimitsUsesConnectedAuthWhenCurrentAuthMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if err := store.Connect(llm.OpenAI, llm.AuthSubscription); err != nil {
		t.Fatalf("Connect(subscription): %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{
		{ID: "gpt-5.5", ContextWindow: 400000, MaxOutput: 16384},
	}); err != nil {
		t.Fatalf("CacheModels(api_key): %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthSubscription, []llm.ModelInfo{
		{ID: "gpt-5.5", ContextWindow: 272000, MaxOutput: 16384},
	}); err != nil {
		t.Fatalf("CacheModels(subscription): %v", err)
	}

	current := &llm.CurrentModelInfo{
		ModelID:  "gpt-5.5",
		Provider: llm.OpenAI,
		// Older providers.json files did not persist authMethod on current.
	}

	for range 30 {
		if got := GetContextWindow(store, current); got != 272000 {
			t.Fatalf("input limit = %d, want 272000 from connected subscription auth", got)
		}
	}
}

// A model whose window San cannot discover resolves to 0, which the status bar
// renders as "--". Inventing a figure would show a percentage of a guess and,
// worse, have compaction act on it.
func TestGetContextWindowUnknownIsZero(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	got := GetContextWindow(store, &llm.CurrentModelInfo{
		ModelID: "unknown-model", Provider: llm.OpenAI, AuthMethod: llm.AuthAPIKey,
	})
	if got != 0 {
		t.Fatalf("GetContextWindow() = %d, want 0 for an undiscoverable window", got)
	}
}

// A hand-set override wins over the cache, and clearing it restores what the
// provider said. The status bar's budget follows it.
func TestContextLimitOverrideWinsAndClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{
		{ID: "m", ContextWindow: 200000, MaxOutput: 8000},
	}); err != nil {
		t.Fatalf("CacheModels: %v", err)
	}
	current := &llm.CurrentModelInfo{ModelID: "m", Provider: llm.OpenAI, AuthMethod: llm.AuthAPIKey}

	if err := store.SetTokenLimit("m", 1_000_000, 64_000); err != nil {
		t.Fatalf("SetTokenLimit: %v", err)
	}
	if got := GetContextWindow(store, current); got != 1_000_000 {
		t.Fatalf("GetContextWindow() = %d, want the 1000000 override", got)
	}
	if got, want := GetPromptBudget(store, current), llm.PromptBudget(1_000_000, 64_000); got != want {
		t.Fatalf("GetPromptBudget() = %d, want %d", got, want)
	}

	if err := store.ClearTokenLimit("m"); err != nil {
		t.Fatalf("ClearTokenLimit: %v", err)
	}
	if got := GetContextWindow(store, current); got != 200000 {
		t.Fatalf("GetContextWindow() after clear = %d, want the cached 200000", got)
	}
}

// No model selected is genuinely unknown, not a case for guessing — the status
// bar renders 0 as "--" rather than a percentage against an invented window.
func TestGetContextWindowWithoutModelIsZero(t *testing.T) {
	if got := GetContextWindow(nil, nil); got != 0 {
		t.Fatalf("GetContextWindow(nil, nil) = %d, want 0", got)
	}
}
