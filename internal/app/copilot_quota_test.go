package app

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
)

func TestCopilotQuotaLabel(t *testing.T) {
	yes, no := true, false
	full, zero := 100.0, 0.0
	for _, tc := range []struct {
		name      string
		snapshots map[string]llm.CopilotQuotaSnapshot
		want      string
	}{
		{"free", map[string]llm.CopilotQuotaSnapshot{"chat": {HasQuota: &yes, PercentRemaining: &full}, "premium_interactions": {HasQuota: &no, Remaining: &zero}}, "Copilot Chat 100% left"},
		{"exhausted", map[string]llm.CopilotQuotaSnapshot{"chat": {HasQuota: &yes, PercentRemaining: &zero}}, "Copilot Chat 0% left"},
		{"no quota", map[string]llm.CopilotQuotaSnapshot{"chat": {HasQuota: &no, PercentRemaining: &zero}}, "Copilot Chat no quota"},
		{"unlimited", map[string]llm.CopilotQuotaSnapshot{"chat": {Unlimited: true}}, "Copilot Chat unlimited"},
		{"premium", map[string]llm.CopilotQuotaSnapshot{"premium_interactions": {HasQuota: &yes, PercentRemaining: &full}}, "Copilot Premium 100% left"},
		{"unavailable", nil, "Copilot quota unavailable"},
		{"unknown", map[string]llm.CopilotQuotaSnapshot{"chat": {}}, "Copilot quota unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatCopilotQuota(llm.CopilotQuota{Snapshots: tc.snapshots}, time.Time{})
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			rendered := conv.RenderModeStatus(conv.OperationModeParams{ModelName: "gpt-4.1", SubscriptionUsage: got, Width: 160})
			if !strings.Contains(rendered, got) {
				t.Fatalf("status lacks quota: %q", rendered)
			}
			narrow := conv.RenderModeStatus(conv.OperationModeParams{ModelName: "gpt-4.1", SubscriptionUsage: got, Width: 15})
			if strings.Contains(narrow, "Copilot") {
				t.Fatalf("quota should drop as a whole: %q", narrow)
			}
		})
	}
}

func TestCopilotQuotaResetLabel(t *testing.T) {
	now := time.Date(2026, 11, 30, 22, 0, 0, 0, time.UTC)
	full := 100.0
	for _, tc := range []struct {
		name, timestamp, date, suffix string
	}{
		{"timestamp preferred", "2026-12-01T00:00:00Z", "2026-12-02", " · ↻ 2h"},
		{"offset timestamp", "2026-12-01T02:30:00+02:30", "", " · ↻ 2h"},
		{"days", "2026-12-02T22:00:00Z", "", " · ↻ 2d"},
		{"28 days", "2026-12-28T22:00:00Z", "", " · ↻ 28d"},
		{"minutes", "2026-11-30T22:30:00Z", "", " · ↻ 30m"},
		{"less than a minute", "2026-11-30T22:00:30Z", "", " · ↻ 1m"},
		{"date only", "", "2026-12-01", " · ↻ Dec 1"},
		{"invalid timestamp falls back", "invalid", "2026-12-01", " · ↻ Dec 1"},
		{"invalid fields", "invalid", "2026-02-30", ""},
		{"missing fields", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := llm.CopilotQuota{
				Snapshots:    map[string]llm.CopilotQuotaSnapshot{"chat": {PercentRemaining: &full}},
				ResetDateUTC: tc.timestamp,
				ResetDate:    tc.date,
			}
			want := "Copilot Chat 100% left" + tc.suffix
			if got := formatCopilotQuota(quota, now); got != want {
				t.Fatalf("got %q want %q", got, want)
			}
			for _, snapshots := range []map[string]llm.CopilotQuotaSnapshot{nil, {"chat": {}}} {
				quota.Snapshots = snapshots
				if got := formatCopilotQuota(quota, now); got != "Copilot quota unavailable" {
					t.Fatalf("unavailable quota should omit reset: %q", got)
				}
			}
		})
	}
}

func TestCopilotQuotaElapsedReset(t *testing.T) {
	now := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	full := 100.0
	for _, reset := range []time.Time{now, now.Add(-time.Minute)} {
		quota := llm.CopilotQuota{
			Snapshots:    map[string]llm.CopilotQuotaSnapshot{"chat": {PercentRemaining: &full}},
			ResetDateUTC: reset.Format(time.RFC3339),
			ResetDate:    "2026-12-02",
		}
		if got := formatCopilotQuota(quota, now); got != "Copilot quota unknown" {
			t.Fatalf("elapsed reset retained cached allowance: %q", got)
		}
	}
}

func TestCopilotSharedSubscriptionUsageLabel(t *testing.T) {
	m := &model{}
	m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.Copilot}
	m.copilotQuota.quota = llm.CopilotQuota{
		Snapshots: map[string]llm.CopilotQuotaSnapshot{"chat": {Unlimited: true}},
		ResetDate: "2026-12-01",
	}
	want := "Copilot Chat unlimited · ↻ Dec 1"
	if got := m.subscriptionUsageLabel(); got != want {
		t.Fatalf("shared slot = %q, want %q", got, want)
	}
	m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.OpenAI, AuthMethod: llm.AuthSubscription}
	m.subscriptionUsage.active = true
	if got := m.subscriptionUsageLabel(); got != "ChatGPT limits …" {
		t.Fatalf("provider switch retained Copilot quota: %q", got)
	}
	m.env.CurrentModel.AuthMethod = llm.AuthAPIKey
	if got := m.subscriptionUsageLabel(); got != "" {
		t.Fatalf("API-key route displayed subscription quota: %q", got)
	}
}

func TestCopilotQuotaRefreshGate(t *testing.T) {
	m := &model{}
	now := time.Now()
	if m.refreshCopilotQuota(now) != nil || m.copilotQuotaLabel() != "" {
		t.Fatal("non-Copilot should not fetch/display")
	}
	m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.Copilot}
	if m.refreshCopilotQuota(now) == nil {
		t.Fatal("first refresh missing")
	}
	if m.refreshCopilotQuota(now) != nil {
		t.Fatal("duplicate in-flight refresh")
	}
	m.copilotQuota.pending = false
	m.copilotQuota.next = now.Add(copilotQuotaTTL)
	if m.refreshCopilotQuota(now) != nil {
		t.Fatal("cache bypassed")
	}
	if m.refreshCopilotQuota(now.Add(copilotQuotaTTL)) == nil {
		t.Fatal("expired cache not refreshed")
	}
}
