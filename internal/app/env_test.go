package app

import "testing"

func TestResetContextDisplay_PreservesCompressions(t *testing.T) {
	e := &env{Compressions: 3, InputTokens: 100, OutputTokens: 50}
	e.ResetContextDisplay()
	if e.Compressions != 3 {
		t.Errorf("Compressions = %d, want 3 (must survive ResetContextDisplay)", e.Compressions)
	}
	if e.InputTokens != 0 || e.OutputTokens != 0 {
		t.Errorf("InputTokens=%d OutputTokens=%d, want 0/0", e.InputTokens, e.OutputTokens)
	}
}

func TestResetTokens_ZeroesCompressions(t *testing.T) {
	e := &env{Compressions: 5, TurnInputTokens: 80, TurnOutputTokens: 40}
	e.ResetTokens()
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 after ResetTokens", e.Compressions)
	}
	if e.TurnInputTokens != 0 || e.TurnOutputTokens != 0 {
		t.Errorf("turn tokens not zeroed: %d/%d", e.TurnInputTokens, e.TurnOutputTokens)
	}
}

func TestCompressions_StartsAtZero(t *testing.T) {
	e := &env{}
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 at session start", e.Compressions)
	}
}
