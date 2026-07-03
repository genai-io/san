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
		{ID: "gpt-5.5", InputTokenLimit: 400000, OutputTokenLimit: 16384},
	}); err != nil {
		t.Fatalf("CacheModels(api_key): %v", err)
	}
	if err := store.CacheModels(llm.OpenAI, llm.AuthSubscription, []llm.ModelInfo{
		{ID: "gpt-5.5", InputTokenLimit: 272000, OutputTokenLimit: 16384},
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
		if got := GetEffectiveInputLimit(store, current); got != 272000 {
			t.Fatalf("input limit = %d, want 272000 (current provider's cache, not the 400k api_key entry)", got)
		}
	}
}
