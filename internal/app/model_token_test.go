package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/v2/internal/app/conv"
	"github.com/genai-io/san/v2/internal/app/input"
	"github.com/genai-io/san/v2/internal/core"
	"github.com/genai-io/san/v2/internal/task/tracker"
)

func TestOnTokenUsageTracksLatestTurnUsage(t *testing.T) {
	m := &model{}
	m.OnTurnBegin()

	m.OnTokenUsage(&core.InferResponse{TokensIn: 1200, TokensOut: 80})
	if m.env.InputTokens != 1200 || m.env.OutputTokens != 80 {
		t.Fatalf("first token update = in:%d out:%d, want in:1200 out:80", m.env.InputTokens, m.env.OutputTokens)
	}
	if m.env.TurnInputTokens != 1200 || m.env.TurnOutputTokens != 80 {
		t.Fatalf("first turn totals = in:%d out:%d, want in:1200 out:80", m.env.TurnInputTokens, m.env.TurnOutputTokens)
	}

	m.OnTokenUsage(&core.InferResponse{TokensIn: 400, TokensOut: 25})
	if m.env.InputTokens != 400 || m.env.OutputTokens != 25 {
		t.Fatalf("latest token update = in:%d out:%d, want in:400 out:25", m.env.InputTokens, m.env.OutputTokens)
	}
	if m.env.TurnInputTokens != 1600 || m.env.TurnOutputTokens != 105 {
		t.Fatalf("accumulated turn totals = in:%d out:%d, want in:1600 out:105", m.env.TurnInputTokens, m.env.TurnOutputTokens)
	}
}

func TestResumeCommandForSessionRequiresPersistedTranscript(t *testing.T) {
	transcriptPath := filepath.Join(t.TempDir(), "session.jsonl")

	if got := resumeCommandForSession("session-1", transcriptPath); got != "" {
		t.Fatalf("resumeCommandForSession() = %q, want empty command for missing transcript", got)
	}

	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if got := resumeCommandForSession("session-1", transcriptPath); got != "san -r session-1" {
		t.Fatalf("resumeCommandForSession() = %q, want san -r session-1", got)
	}
}

func TestOnTokenUsageClearsCompactedStatusOnNextInfer(t *testing.T) {
	m := &model{}
	m.userInput.Provider.StatusMessage = "compacted"

	m.OnTokenUsage(&core.InferResponse{TokensIn: 400, TokensOut: 25})

	if m.userInput.Provider.StatusMessage != "" {
		t.Fatalf("StatusMessage = %q, want compacted badge cleared on next infer", m.userInput.Provider.StatusMessage)
	}
}

func TestOnTurnBeginResetsTurnTotalsOnlyForNewTurn(t *testing.T) {
	m := &model{}
	m.env.TurnInputTokens = 1600
	m.env.TurnOutputTokens = 105

	m.env.turnUsageActive = true
	m.OnTurnBegin()
	if m.env.TurnInputTokens != 1600 || m.env.TurnOutputTokens != 105 {
		t.Fatalf("existing turn totals changed unexpectedly: in:%d out:%d", m.env.TurnInputTokens, m.env.TurnOutputTokens)
	}

	m.env.turnUsageActive = false
	m.OnTurnBegin()
	if m.env.TurnInputTokens != 0 || m.env.TurnOutputTokens != 0 {
		t.Fatalf("new turn reset = in:%d out:%d, want zeros", m.env.TurnInputTokens, m.env.TurnOutputTokens)
	}
}

func TestResetContextDisplayPreservesTurnTotals(t *testing.T) {
	m := &model{}
	m.env.InputTokens = 1200
	m.env.OutputTokens = 80
	m.env.TurnInputTokens = 1600
	m.env.TurnOutputTokens = 105

	m.env.ResetContextDisplay()

	if m.env.InputTokens != 0 || m.env.OutputTokens != 0 {
		t.Fatalf("context display reset = in:%d out:%d, want zeros", m.env.InputTokens, m.env.OutputTokens)
	}
	if m.env.TurnInputTokens != 1600 || m.env.TurnOutputTokens != 105 {
		t.Fatalf("turn totals changed unexpectedly: in:%d out:%d", m.env.TurnInputTokens, m.env.TurnOutputTokens)
	}
}

// OnAgentMessage observes the agent's MessageEvent echoes only — every
// path that hands a user message to the agent (idle submit, drainTurnQueues,
// cron prompt, async hook) appends to m.conv at the call site. The echo must
// be a strict no-op or the conversation double-displays.
func TestOnAgentMessageIsNoOpForUserEcho(t *testing.T) {
	m := &model{
		userInput: input.Model{Queue: input.NewQueue()},
		conv:      conv.NewModel(80),
		services:  services{Tracker: tracker.NewStore()},
	}

	_ = m.OnAgentMessage(core.UserMessage("anything", nil))

	if len(m.conv.Messages) != 0 {
		t.Fatalf("conv messages = %d, want 0 (echo must not append)", len(m.conv.Messages))
	}
}
