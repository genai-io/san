package agent

import (
	"testing"
)

// The context window resolved by the app (user override, then the model store's
// cache) has to reach the client the agent infers with — ThinkAct's compaction
// check is the only reader of InputLimit, and it treats a missing value as
// "unknown", silently disabling auto-compaction. stubProvider reports no model
// limits, so anything other than the caller's figure means the wiring is
// broken. Regression guard for #338.
func TestNewLLMClientCarriesResolvedInputLimit(t *testing.T) {
	c := newLLMClient(BuildParams{Provider: stubProvider{}, ModelID: "m", InputLimit: 272_000})

	if got := c.InputLimit(); got != 272_000 {
		t.Fatalf("InputLimit() = %d, want the resolved 272000", got)
	}
}

// A caller that resolved nothing must leave the client on its own provider
// lookup rather than pinning it to zero.
func TestNewLLMClientWithoutInputLimitFallsBackToProvider(t *testing.T) {
	c := newLLMClient(BuildParams{Provider: stubProvider{}, ModelID: "m"})

	if got := c.InputLimit(); got != 0 {
		t.Fatalf("InputLimit() = %d, want 0 from a provider that reports no limits", got)
	}
}
