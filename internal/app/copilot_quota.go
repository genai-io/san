package app

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/llm"
)

const copilotQuotaTTL = 2 * time.Minute

type copilotQuotaState struct {
	quota   llm.CopilotQuota
	pending bool
	next    time.Time
}
type copilotQuotaMsg struct {
	quota llm.CopilotQuota
	err   error
}
type copilotQuotaTick struct{}

func copilotQuotaTicker() tea.Cmd {
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { return copilotQuotaTick{} })
}

func (m *model) usesCopilot() bool {
	return m.env.CurrentModel != nil && m.env.CurrentModel.Provider == llm.Copilot
}

// The UI owns the short cache (including failures) and allows only one fetch
// in flight. Rendering and inference never perform network work.
func (m *model) refreshCopilotQuota(now time.Time) tea.Cmd {
	if !m.usesCopilot() || m.copilotQuota.pending || now.Before(m.copilotQuota.next) {
		return nil
	}
	m.copilotQuota.pending = true
	return func() tea.Msg {
		quota, err := llm.FetchCopilotQuota(context.Background())
		return copilotQuotaMsg{quota, err}
	}
}

func (m *model) copilotQuotaLabel() string {
	if !m.usesCopilot() {
		return ""
	}
	return formatCopilotQuota(m.copilotQuota.quota, time.Now())
}

func formatCopilotQuota(quota llm.CopilotQuota, now time.Time) string {
	// Prefer a finite premium allowance when present, otherwise the Chat bucket
	// (free accounts may have no premium allowance at all).
	name := "Chat"
	q, ok := quota.Snapshots["chat"]
	if premium, exists := quota.Snapshots["premium_interactions"]; exists && premium.HasQuota != nil && *premium.HasQuota && !premium.Unlimited {
		name, q, ok = "Premium", premium, true
	}
	prefix := "Copilot " + name + " "
	if !ok {
		return "Copilot quota unavailable"
	}
	reset, expired := formatCopilotQuotaReset(quota, now)
	if expired {
		// Like ChatGPT windows, a reset invalidates the cached allowance.
		return "Copilot quota unknown"
	}
	if q.Unlimited {
		return prefix + "unlimited" + reset
	}
	if q.HasQuota != nil && !*q.HasQuota {
		return prefix + "no quota" + reset
	}
	if q.PercentRemaining != nil && *q.PercentRemaining >= 0 && *q.PercentRemaining <= 100 {
		return prefix + fmt.Sprintf("%g%% left", *q.PercentRemaining) + reset
	}
	if q.Remaining != nil && *q.Remaining >= 0 {
		return prefix + fmt.Sprintf("%g left", *q.Remaining) + reset
	}
	return "Copilot quota unavailable"
}

func formatCopilotQuotaReset(quota llm.CopilotQuota, now time.Time) (suffix string, expired bool) {
	if reset, err := time.Parse(time.RFC3339, quota.ResetDateUTC); err == nil {
		if !reset.After(now) {
			return "", true
		}
		return " (" + compactUsageDuration(reset.Sub(now)) + ")", false
	}
	// A date-only fallback does not establish a reset time or timezone.
	if reset, err := time.Parse(time.DateOnly, quota.ResetDate); err == nil {
		return " (" + reset.Format("Jan 2") + ")", false
	}
	return "", false
}
