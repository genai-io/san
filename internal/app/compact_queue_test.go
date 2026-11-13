package app

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestSubmitDuringCompactionQueuesInput(t *testing.T) {
	for _, raw := range []string{"continue after compaction", "look at [Image #1]", "[Image #1]"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			m := fixedComposerModel(80, 24)
			m.OnCompactStart(108)
			m.userInput.Textarea.SetValue(raw)
			images := []core.Attachment{}
			if strings.Contains(raw, "[Image #1]") {
				images = []core.Attachment{chartImage()}
				m.userInput.Images.Pending = []input.PendingImage{{ID: 1, Data: images[0]}}
			}
			m.handleSubmit()
			if m.userInput.Queue.Len() != 1 {
				t.Fatalf("input during compaction must enter the pending queue, got %d queued messages", m.userInput.Queue.Len())
			}
			item, _ := m.userInput.Queue.At(0)
			if item.Content != raw || !reflect.DeepEqual(item.Images, images) || m.userInput.Textarea.Value() != "" || len(m.userInput.Images.Pending) != 0 {
				t.Fatal("queued input was changed or left in the composer")
			}
			if cmd := m.sendNextQueuedInputIfIdle(); cmd != nil {
				t.Fatal("idle drain released input while compaction was active")
			}
			if _, released := m.releaseQueuedMessage(); released || m.userInput.Queue.Len() != 1 {
				t.Fatal("step/turn drain released input while compaction was active")
			}
			if len(m.conv.Messages) != 0 || !m.conv.Compact.Active {
				t.Fatal("queueing input changed the conversation or ended compaction")
			}
		})
	}
}

func compactQueueModel(t *testing.T) (*model, *textOnlyStubProvider) {
	t.Helper()
	m, _ := resumedModel(t, parallelResultChain())
	m.userInput = input.New(m.env.CWD, 80, nil, input.SelectorDeps{Setting: m.services.Setting})
	provider := &textOnlyStubProvider{restartStubProvider{requests: make(chan []core.Message, 2)}}
	m.env.LLMProvider = provider
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", Recording: m.services.Session, PermissionRules: (agent.PermissionPolicy{}).Decide}, m.services.Agent.Messages()); err != nil {
		t.Fatal(err)
	}
	m.agentEvolveCaps = m.selfLearnCapabilities()
	m.agentDisabledToolsSignature = m.disabledToolsSignature()
	m.agentAutoMemory = m.autoMemory()
	return m, provider
}

func TestCompactionReleasesOneQueuedInput(t *testing.T) {
	for _, trigger := range []string{"manual", "auto", "nothing"} {
		t.Run(trigger, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			const summary = "both files read"
			m.env.LLMProvider = &compactHTTPProvider{client: aitest.New(aitest.Says(summary)).Client()}
			var request agent.CompactRequest
			if trigger == "auto" {
				request = agent.CompactRequest{Ctx: t.Context(), Client: m.buildLLMClient(), Messages: m.services.Agent.Messages(), Completion: m.services.Session.Completion(session.PurposeCompaction), Trigger: "auto"}
			} else {
				request = m.BuildCompactRequest("", "manual")
			}
			m.env.LLMProvider = provider
			m.OnCompactStart(len(m.conv.Messages))
			m.userInput.Textarea.SetValue("look at [Image #1]")
			m.userInput.Images.Pending = []input.PendingImage{{ID: 1, Data: chartImage()}}
			m.handleSubmit()
			m.userInput.Textarea.SetValue("second queued input")
			m.handleSubmit()
			m.userInput.Textarea.SetValue("draft [Image #2]")
			m.userInput.Images.Pending = []input.PendingImage{{ID: 2, Data: chartImage()}}
			m.userInput.Images.NextID = 2

			expectedSummary := ""
			switch trigger {
			case "manual", "auto":
				msg := conv.CompactCmd(request)().(conv.CompactResultMsg)
				if msg.Err != nil {
					t.Fatal(msg.Err)
				}
				expectedSummary = msg.Summary
				if !strings.Contains(expectedSummary, summary) {
					t.Fatalf("model summary lost: %q", expectedSummary)
				}
				if trigger == "manual" {
					m.HandleCompactResult(msg)
				} else {
					accepted, err := m.services.Agent.Compact(msg.Completion.Compaction(msg.Summary))
					if err != nil || !accepted {
						t.Fatalf("automatic compaction admission: accepted=%v err=%v", accepted, err)
					}
				}
				if m.userInput.Queue.Len() != 2 {
					t.Fatal("manual input was released before the replacement reached the UI")
				}
				deadline := time.After(2 * time.Second)
			waitBoundary:
				for {
					select {
					case event := <-m.services.Agent.Outbox():
						if info, ok := event.(core.Compacted); ok {
							m.OnCompacted(info)
							break waitBoundary
						}
					case <-deadline:
						t.Fatal("manual compaction never applied its summary")
					}
				}
				if trigger == "auto" {
					m.OnCompactEnd(sdkagent.CompactionEnd{})
				}
			case "nothing":
				request.Messages = nil
				msg := conv.CompactCmd(request)().(conv.CompactResultMsg)
				if !errors.Is(msg.Err, agent.ErrNothingToCompact) {
					t.Fatalf("expected short-input rejection: %v", msg.Err)
				}
				m.HandleCompactResult(msg)
			}

			select {
			case request := <-provider.requests:
				last := request[len(request)-1]
				if !strings.Contains(last.Text(), "look at") || !strings.Contains(last.Text(), "/tmp/chart.png") || imgCount(last) != 0 {
					t.Fatal("queued text or image did not reach inference")
				}
				if trigger != "nothing" && (len(request) != 2 || request[0].Text() != core.FormatCompactSummary(expectedSummary)) {
					t.Fatal("queued inference used the old history instead of the summary")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("queued input never resumed after compaction")
			}
			var queuedImages int
			for _, message := range m.services.Agent.Messages() {
				if strings.Contains(message.Text(), "look at") {
					queuedImages += imgCount(message)
				}
			}
			if queuedImages != 1 {
				t.Fatal("queued inline image was dropped or duplicated in native history")
			}
			if item, ok := m.userInput.Queue.At(0); !ok || item.Content != "second queued input" || m.userInput.Queue.Len() != 1 {
				t.Fatal("completion did not release exactly one input in FIFO order")
			}
			if m.userInput.Textarea.Value() != "draft [Image #2]" || m.userInput.Images.NextID != 2 || !reflect.DeepEqual(m.userInput.Images.Pending, []input.PendingImage{{ID: 2, Data: chartImage()}}) {
				t.Fatal("completion changed the unrelated draft or its image")
			}
		})
	}
}

func TestCompactionQueueEditWaitsForBothCompletionAndEditExit(t *testing.T) {
	for _, order := range []string{"edit-first", "compact-first"} {
		t.Run(order, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.env.LLMProvider = &compactHTTPProvider{client: aitest.New(aitest.Says("summary")).Client()}
			request := agent.CompactRequest{Ctx: t.Context(), Client: m.buildLLMClient(), Messages: m.services.Agent.Messages(), Completion: m.services.Session.Completion(session.PurposeCompaction), Trigger: "auto"}
			m.env.LLMProvider = provider
			m.OnCompactStart(3)
			m.userInput.Textarea.SetValue("queued input")
			m.handleSubmit()
			m.userInput.Textarea.SetValue("unrelated draft")
			m.userInput.EnterQueueSelection()
			m.userInput.Textarea.SetValue("edited input")
			finish := func() {
				result := request.Run()
				if result.Err != nil {
					t.Fatal(result.Err)
				}
				accepted, err := m.services.Agent.Compact(result.Completion.Compaction(result.Summary))
				if err != nil || !accepted {
					t.Fatalf("automatic compaction: %v %v", accepted, err)
				}
				deadline := time.After(2 * time.Second)
				for {
					select {
					case event := <-m.AgentOutbox():
						if info, ok := event.(core.Compacted); ok {
							m.OnCompacted(info)
							return
						}
					case <-deadline:
						t.Fatal("automatic summary boundary missing")
					}
				}
			}
			exit := func() { m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEnter}) }
			first, second := exit, finish
			if order == "compact-first" {
				first, second = finish, exit
			}
			first()
			if m.userInput.Queue.Len() != 1 {
				t.Fatal("input released while compaction or head edit was still active")
			}
			second()
			select {
			case request := <-provider.requests:
				if !strings.Contains(request[len(request)-1].Text(), "edited input") {
					t.Fatal("input was dispatched before the edit was saved")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("edited input did not resume")
			}
			if m.userInput.Queue.Len() != 0 || m.userInput.Textarea.Value() != "unrelated draft" {
				t.Fatal("edit exit lost the draft or duplicated the queued input")
			}
		})
	}
}

func TestFailedCompactionKeepsQueuedInput(t *testing.T) {
	for _, trigger := range []string{"manual", "auto"} {
		t.Run(trigger, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.env.LLMProvider = &compactHTTPProvider{client: aitest.New(aitest.Fails(&ai.Error{Kind: ai.KindAuth, Message: "summary failed"})).Client()}
			request := m.BuildCompactRequest("", "manual")
			m.env.LLMProvider = provider
			m.OnCompactStart(3)
			m.userInput.Textarea.SetValue("look at [Image #1]")
			m.userInput.Images.Pending = []input.PendingImage{{ID: 1, Data: chartImage()}}
			m.handleSubmit()
			msg := conv.CompactCmd(request)().(conv.CompactResultMsg)
			if msg.Err == nil {
				t.Fatal("expected actual summary failure")
			}
			if trigger == "manual" {
				m.HandleCompactResult(msg)
			} else {
				m.OnCompactEnd(sdkagent.CompactionEnd{Err: msg.Err})
			}
			item, ok := m.userInput.Queue.At(0)
			if !ok || item.Content != "look at [Image #1]" || !reflect.DeepEqual(item.Images, []core.Attachment{chartImage()}) || m.conv.Compact.Active {
				t.Fatal("failed compaction lost input or left compaction active")
			}
			select {
			case <-provider.requests:
				t.Fatal("failed compaction retried queued input against the old history")
			default:
			}
		})
	}
}
