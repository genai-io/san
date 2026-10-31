package app

import (
	"errors"
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
	if m.syncSubscriptionUsage() != nil || m.refreshSubscriptionUsage() != nil {
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

func TestFormatSubscriptionUsage(t *testing.T) {
	now := time.Unix(2000000000, 0)
	usage := auth.CodexUsage{Windows: []auth.CodexUsageWindow{
		{Duration: 7 * 24 * time.Hour, UsedPercent: 100, ResetsAt: now.Add(48 * time.Hour)},
		{Duration: 5 * time.Hour, UsedPercent: 0},
	}}
	if got := formatSubscriptionUsage(usage, now); got != "ChatGPT week 0% left (2d) / 5h 100% left" {
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
