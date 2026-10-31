package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai/auth"
)

const usageRefreshInterval = 5 * time.Minute

type subscriptionUsageState struct {
	active     bool
	generation uint64
	cancel     context.CancelFunc
	usage      auth.CodexUsage
	checked    bool
	failed     bool
}
type subscriptionUsageMsg struct {
	generation uint64
	usage      auth.CodexUsage
	err        error
}
type subscriptionUsageTick struct{ generation uint64 }

// Observe route changes after every update; model switches within the same
// subscription share the account snapshot. Leaving it invalidates all work.
func (m *model) syncSubscriptionUsage() tea.Cmd {
	active := llm.ChatGPTSubscription(m.env.CurrentModel)
	if active == m.subscriptionUsage.active {
		return nil
	}
	generation := m.subscriptionUsage.generation + 1
	if m.subscriptionUsage.cancel != nil {
		m.subscriptionUsage.cancel()
	}
	m.subscriptionUsage = subscriptionUsageState{active: active, generation: generation}
	if !active {
		return nil
	}
	return m.refreshSubscriptionUsage()
}

func (m *model) refreshSubscriptionUsage() tea.Cmd {
	if m.subscriptionUsage.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.subscriptionUsage.cancel = cancel
	generation := m.subscriptionUsage.generation
	return func() tea.Msg {
		defer cancel()
		usage, err := llm.ChatGPTUsage(ctx)
		return subscriptionUsageMsg{generation: generation, usage: usage, err: err}
	}
}

func (m *model) handleSubscriptionUsage(msg subscriptionUsageMsg) tea.Cmd {
	if !llm.ChatGPTSubscription(m.env.CurrentModel) || !m.subscriptionUsage.active || msg.generation != m.subscriptionUsage.generation {
		return nil
	}
	if m.subscriptionUsage.cancel != nil {
		m.subscriptionUsage.cancel()
	}
	m.subscriptionUsage.cancel = nil
	m.subscriptionUsage.checked = true
	m.subscriptionUsage.failed = msg.err != nil
	m.subscriptionUsage.usage = msg.usage
	return tea.Tick(usageRefreshInterval, func(time.Time) tea.Msg { return subscriptionUsageTick{generation: msg.generation} })
}

func (m *model) handleSubscriptionUsageTick(msg subscriptionUsageTick) tea.Cmd {
	if !llm.ChatGPTSubscription(m.env.CurrentModel) || !m.subscriptionUsage.active || msg.generation != m.subscriptionUsage.generation {
		return nil
	}
	return m.refreshSubscriptionUsage()
}

func (m model) subscriptionUsageLabel() string {
	if !llm.ChatGPTSubscription(m.env.CurrentModel) || !m.subscriptionUsage.active {
		return ""
	}
	if !m.subscriptionUsage.checked {
		return "ChatGPT limits …"
	}
	if m.subscriptionUsage.failed {
		return "ChatGPT limits unavailable"
	}
	return formatSubscriptionUsage(m.subscriptionUsage.usage, time.Now())
}

func formatSubscriptionUsage(usage auth.CodexUsage, now time.Time) string {
	var parts []string
	for _, w := range usage.Windows {
		// Once a window resets, its cached percentage is no longer authoritative.
		if !w.ResetsAt.IsZero() && !w.ResetsAt.After(now) {
			continue
		}
		label := fmt.Sprintf("%gm", w.Duration.Minutes())
		if w.Duration%time.Hour == 0 {
			label = fmt.Sprintf("%gh", w.Duration.Hours())
		}
		if w.Duration%(24*time.Hour) == 0 {
			label = fmt.Sprintf("%gd", w.Duration.Hours()/24)
		}
		if w.Duration == 7*24*time.Hour {
			label = "week"
		}
		text := fmt.Sprintf("%s %.0f%% left", label, 100-w.UsedPercent)
		if !w.ResetsAt.IsZero() {
			text += " (" + compactUsageDuration(w.ResetsAt.Sub(now)) + ")"
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "ChatGPT limits unknown"
	}
	return "ChatGPT " + strings.Join(parts, " / ")
}

func compactUsageDuration(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", max(1, int(d.Minutes())))
}
