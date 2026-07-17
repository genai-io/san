package app

import (
	"testing"

	"github.com/genai-io/san/internal/llm"
)

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

// ResetContextDisplay runs on every (auto-)compaction; the session-cumulative
// cost must survive it, otherwise a long session's displayed spend resets to
// zero each time the context is compacted.
func TestResetContextDisplay_PreservesConversationCost(t *testing.T) {
	cost := llm.Money{Amount: 1.25, Currency: llm.CurrencyUSD}
	e := &env{InputTokens: 100, OutputTokens: 50, ConversationCost: cost}
	e.ResetContextDisplay()
	if e.ConversationCost != cost {
		t.Errorf("ConversationCost = %v, want %v (must survive ResetContextDisplay)", e.ConversationCost, cost)
	}
}

func TestResetTokens_ZeroesCompressions(t *testing.T) {
	e := &env{Compressions: 5, InputTokens: 80, OutputTokens: 40}
	e.ResetTokens()
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 after ResetTokens", e.Compressions)
	}
	if e.InputTokens != 0 || e.OutputTokens != 0 {
		t.Errorf("context tokens not zeroed: %d/%d", e.InputTokens, e.OutputTokens)
	}
}

func TestResetTokens_ZeroesConversationCost(t *testing.T) {
	e := &env{ConversationCost: llm.Money{Amount: 2.50, Currency: llm.CurrencyUSD}}
	e.ResetTokens()
	if !e.ConversationCost.IsZero() {
		t.Errorf("ConversationCost = %v, want zero after ResetTokens", e.ConversationCost)
	}
}

func TestCompressions_StartsAtZero(t *testing.T) {
	e := &env{}
	if e.Compressions != 0 {
		t.Errorf("Compressions = %d, want 0 at session start", e.Compressions)
	}
}
