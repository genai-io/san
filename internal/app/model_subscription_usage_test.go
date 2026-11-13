package app

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai/auth"
)

func TestSubscriptionUsageLifecycle(t *testing.T) {
	m := &model{}
	if m.syncSubscriptionUsage() != nil {
		t.Fatal("API route fetched")
	}
	m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.OpenAI, AuthMethod: llm.AuthSubscription}
	if m.syncSubscriptionUsage() == nil {
		t.Fatal("missing initial fetch")
	}
	defer func() {
		if m.subscriptionUsage.cancel != nil {
			m.subscriptionUsage.cancel()
		}
	}()
	generation := m.subscriptionUsage.generation
	if m.syncSubscriptionUsage() != nil || m.refreshSubscriptionUsage(time.Now()) != nil {
		t.Fatal("duplicate fetch")
	}
	if m.subscriptionUsageLabel() != "ChatGPT limits …" {
		t.Fatal("missing loading state")
	}
	msg := subscriptionUsageMsg{generation: generation, usage: auth.CodexUsage{Windows: []auth.CodexUsageWindow{{Duration: 5 * time.Hour, UsedPercent: 20}}}}
	if m.handleSubscriptionUsage(msg) == nil || m.subscriptionUsage.cancel != nil {
		t.Fatal("did not schedule refresh")
	}
	if !strings.Contains(m.subscriptionUsageLabel(), "5h 80% left") {
		t.Fatal(m.subscriptionUsageLabel())
	}
	if m.syncSubscriptionUsage() != nil {
		t.Fatal("cache not used")
	}
	if m.handleSubscriptionUsageTick(subscriptionUsageTick{generation}) != nil {
		t.Fatal("early tick bypassed refresh deadline")
	}
	m.subscriptionUsage.next = time.Now().Add(-time.Second)
	if m.handleSubscriptionUsageTick(subscriptionUsageTick{generation}) == nil {
		t.Fatal("no periodic refresh")
	}
	m.handleSubscriptionUsage(subscriptionUsageMsg{generation: generation, err: errors.New("offline")})
	if m.subscriptionUsageLabel() != "ChatGPT limits unavailable" {
		t.Fatal("error shown as quota")
	}
	m.env.CurrentModel.AuthMethod = llm.AuthAPIKey
	if m.subscriptionUsageLabel() != "" {
		t.Fatal("not immediately hidden")
	}
	m.syncSubscriptionUsage()
	if m.handleSubscriptionUsage(msg) != nil || m.handleSubscriptionUsageTick(subscriptionUsageTick{generation}) != nil {
		t.Fatal("stale update accepted")
	}
	m.env.CurrentModel.AuthMethod = llm.AuthSubscription
	if m.syncSubscriptionUsage() == nil {
		t.Fatal("no fetch after switching back")
	}
	if m.handleSubscriptionUsage(msg) != nil || m.subscriptionUsage.checked {
		t.Fatal("previous subscription generation accepted")
	}
}

func TestSubscriptionUsageIntervals(t *testing.T) {
	for _, tc := range []struct {
		name string
		used []float64
		want time.Duration
	}{
		{"unknown", nil, time.Minute},
		{"unused", []float64{0}, time.Minute},
		{"below 75", []float64{74.99}, time.Minute},
		{"75", []float64{75}, 30 * time.Second},
		{"below 90", []float64{89.99}, 30 * time.Second},
		{"90", []float64{90}, 15 * time.Second},
		{"below 99", []float64{98.99}, 15 * time.Second},
		{"99", []float64{99}, 5 * time.Second},
		{"exhausted", []float64{100}, 5 * time.Second},
		{"weekly higher", []float64{20, 95}, 15 * time.Second},
		{"short window higher", []float64{99, 20}, 5 * time.Second},
		{"invalid windows", []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 101, 80}, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := auth.CodexUsage{}
			for i, used := range tc.used {
				usage.Windows = append(usage.Windows, auth.CodexUsageWindow{Duration: time.Duration(i+1) * time.Hour, UsedPercent: used})
			}
			if got := usageRefreshInterval(usage); got != tc.want {
				t.Fatalf("interval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubscriptionUsageAdaptiveScheduling(t *testing.T) {
	m := &model{}
	m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.OpenAI, AuthMethod: llm.AuthSubscription}
	m.syncSubscriptionUsage()
	defer func() {
		if m.subscriptionUsage.cancel != nil {
			m.subscriptionUsage.cancel()
		}
	}()
	generation := m.subscriptionUsage.generation
	for _, used := range []float64{20, 75, 90, 99, 100, 10} {
		usage := auth.CodexUsage{Windows: []auth.CodexUsageWindow{{Duration: 5 * time.Hour, UsedPercent: used}}}
		interval := usageRefreshInterval(usage)
		before := time.Now()
		if m.handleSubscriptionUsage(subscriptionUsageMsg{generation: generation, usage: usage}) == nil {
			t.Fatal("missing scheduled refresh")
		}
		after := time.Now()
		next := m.subscriptionUsage.next
		if next.Before(before.Add(interval)) || next.After(after.Add(interval)) {
			t.Fatalf("used=%v: deadline=%v, expected interval=%v", used, next, interval)
		}
		if m.syncSubscriptionUsage() != nil || m.handleSubscriptionUsageTick(subscriptionUsageTick{generation}) != nil ||
			m.refreshSubscriptionUsage(next.Add(-time.Nanosecond)) != nil {
			t.Fatal("cached response or old timer caused an early request")
		}
		if m.refreshSubscriptionUsage(next) == nil {
			t.Fatal("deadline did not admit a refresh")
		}
		if m.refreshSubscriptionUsage(next) != nil {
			t.Fatal("duplicate in-flight request")
		}
	}
}

func TestSubscriptionUsageFailureKeepsCadence(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial failure", true: "near exhaustion"}[known], func(t *testing.T) {
			m := &model{}
			m.env.CurrentModel = &llm.CurrentModelInfo{Provider: llm.OpenAI, AuthMethod: llm.AuthSubscription}
			m.syncSubscriptionUsage()
			defer func() {
				if m.subscriptionUsage.cancel != nil {
					m.subscriptionUsage.cancel()
				}
			}()
			generation := m.subscriptionUsage.generation
			interval := time.Minute
			if known {
				interval = 5 * time.Second
				usage := auth.CodexUsage{Windows: []auth.CodexUsageWindow{{Duration: 5 * time.Hour, UsedPercent: 99}}}
				m.handleSubscriptionUsage(subscriptionUsageMsg{generation: generation, usage: usage})
				m.refreshSubscriptionUsage(m.subscriptionUsage.next)
			}
			before := time.Now()
			if m.handleSubscriptionUsage(subscriptionUsageMsg{generation: generation, err: errors.New("offline")}) == nil {
				t.Fatal("failure stopped polling")
			}
			after := time.Now()
			next := m.subscriptionUsage.next
			if next.Before(before.Add(interval)) || next.After(after.Add(interval)) {
				t.Fatal("failure lost the prior quota cadence")
			}
			if m.subscriptionUsageLabel() != "ChatGPT limits unavailable" {
				t.Fatal("failed query displayed old data as fresh")
			}
			if m.syncSubscriptionUsage() != nil || m.handleSubscriptionUsageTick(subscriptionUsageTick{generation}) != nil {
				t.Fatal("failure caused an immediate retry loop")
			}
			m.subscriptionUsage.next = time.Now().Add(-time.Second)
			if m.syncSubscriptionUsage() == nil {
				t.Fatal("an overdue refresh requires no particular timer message")
			}
		})
	}
}

func TestCompactUsageDuration(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "<1m"},
		{"subsecond", time.Nanosecond, "<1m"},
		{"below minute", time.Minute - time.Nanosecond, "<1m"},
		{"minute", time.Minute, "1m"},
		{"minutes with seconds", 35*time.Minute + 59*time.Second, "35m"},
		{"below hour", time.Hour - time.Nanosecond, "59m"},
		{"hour", time.Hour, "1h"},
		{"hour with seconds", time.Hour + 59*time.Second, "1h"},
		{"hours and minutes", 2*time.Hour + 35*time.Minute + 59*time.Second, "2h 35m"},
		{"below day", 24*time.Hour - time.Nanosecond, "23h 59m"},
		{"day", 24 * time.Hour, "1d"},
		{"day with minutes", 24*time.Hour + 59*time.Minute + 59*time.Second, "1d"},
		{"day and hour", 25 * time.Hour, "1d 1h"},
		{"28 days", 28 * 24 * time.Hour, "28d"},
		{"691 hours", 691 * time.Hour, "28d 19h"},
		{"691 hours with seconds", 691*time.Hour + 59*time.Second, "28d 19h"},
		{"691 hours with minutes and seconds", 691*time.Hour + 35*time.Minute + 59*time.Second, "28d 19h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := compactUsageDuration(tc.d); got != tc.want {
				t.Fatalf("compactUsageDuration(%s) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestFormatSubscriptionUsage(t *testing.T) {
	now := time.Unix(2000000000, 0)
	usage := auth.CodexUsage{Windows: []auth.CodexUsageWindow{
		{Duration: 7 * 24 * time.Hour, UsedPercent: 100, ResetsAt: now.Add(48 * time.Hour)},
		{Duration: 5 * time.Hour, UsedPercent: 0},
	}}
	if got := formatSubscriptionUsage(usage, now); got != "ChatGPT week 0% left (2d) / 5h 100% left" {
		t.Fatal(got)
	}
	example := auth.CodexUsage{Windows: []auth.CodexUsageWindow{
		{Duration: 5 * time.Hour, UsedPercent: 10, ResetsAt: now.Add(2*time.Hour + 35*time.Minute + 59*time.Second)},
	}}
	if got := formatSubscriptionUsage(example, now); got != "ChatGPT 5h 90% left (2h 35m)" {
		t.Fatal(got)
	}
	if got := formatSubscriptionUsage(auth.CodexUsage{}, now); got != "ChatGPT limits unknown" {
		t.Fatal(got)
	}
	usage.Windows = usage.Windows[:1]
	if got := formatSubscriptionUsage(usage, now.Add(49*time.Hour)); got != "ChatGPT limits unknown" {
		t.Fatal(got)
	}
}
