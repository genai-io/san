package llm

import "testing"

func TestEstimateCostDispatchesToRegisteredProvider(t *testing.T) {
	const fake Name = "test-cost-provider"
	want := Money{Amount: 1.5, Currency: CurrencyUSD}
	RegisterCostEstimator(fake, func(modelID string, usage Usage) (Money, bool) {
		if modelID != "m1" {
			t.Fatalf("modelID = %q, want m1", modelID)
		}
		if usage.InputTokens != 10 {
			t.Fatalf("usage.InputTokens = %d, want 10", usage.InputTokens)
		}
		return want, true
	})

	got, ok := EstimateCost(fake, "m1", Usage{InputTokens: 10})
	if !ok || got != want {
		t.Fatalf("EstimateCost = (%+v, %v), want (%+v, true)", got, ok, want)
	}
}

func TestEstimateCostUnknownProviderReturnsFalse(t *testing.T) {
	cost, ok := EstimateCost("totally-unregistered-provider", "m", Usage{})
	if ok || !cost.IsZero() {
		t.Fatalf("EstimateCost(unknown) = (%+v, %v), want (zero, false)", cost, ok)
	}
}
