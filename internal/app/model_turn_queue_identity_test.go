package app

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
)

func TestAsyncHookContinuationKeepsContextInDeliveredTurn(t *testing.T) {
	m, provider := noticeDeliveryModel(t)
	m.env.LLMProvider = provider
	m.services.Setting = setting.New(setting.NewData())
	m.agentEvolveCaps = m.selfLearnCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	cmd := m.injectAsyncHookContinuation(trigger.AsyncHookRewake{
		Context:            []string{"policy finding", "tool detail"},
		ContinuationPrompt: "Re-evaluate the plan",
	})
	rawResult := cmd()
	result, ok := rawResult.(agentSendResultMsg)
	if !ok {
		t.Fatalf("continuation command result = %T, want agentSendResultMsg", rawResult)
	}
	if result.err != nil {
		t.Fatalf("deliver continuation: %v", result.err)
	}

	var got core.ChatMessage
	for i := len(m.conv.Messages) - 1; i >= 0; i-- {
		if m.conv.Messages[i].Role == core.ChatUser {
			got = m.conv.Messages[i]
			break
		}
	}
	for _, want := range []string{"policy finding", "tool detail", "Re-evaluate the plan"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("continuation input %q is missing %q", got.Content, want)
		}
	}
	if got.ID == "" {
		t.Fatal("continuation input has no stable message ID")
	}

	select {
	case request := <-provider.requests:
		if len(request) != 1 {
			t.Fatalf("delivered continuation = %+v, want one user turn", request)
		}
		for _, want := range []string{"policy finding", "tool detail", "Re-evaluate the plan"} {
			if !strings.Contains(request[0].Text(), want) {
				t.Fatalf("delivered continuation %q is missing %q", request[0].Text(), want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async hook continuation inference")
	}
}
