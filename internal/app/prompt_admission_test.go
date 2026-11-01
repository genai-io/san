package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func installBlockingPromptHook(t *testing.T, m *model) *int {
	t.Helper()
	m.services.Setting = setting.New(setting.NewData())
	m.services.Hook = hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
	calls := new(int)
	m.services.Hook.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{
		Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
			*calls++
			allow := false
			return hook.HookOutput{Continue: &allow, StopReason: "test policy", SystemMessage: "context from " + in.Prompt}, nil
		},
	})
	return calls
}

func TestPromptAdmissionBlocksDirectAndIdleQueuedBeforeSlash(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "idle queue"}[queued], func(t *testing.T) {
			m, _ := textOnlyModel(t)
			m.userInput = input.New("", 80, nil, input.SelectorDeps{})
			calls := installBlockingPromptHook(t, m)
			if queued {
				m.userInput.Textarea.SetValue("unrelated draft")
				m.userInput.Queue.Enqueue("/clear", nil)
				m.sendNextQueuedInputIfIdle()
				if m.userInput.Textarea.Value() != "unrelated draft" {
					t.Fatal("lost composer draft")
				}
			} else {
				m.userInput.Textarea.SetValue("/clear")
				m.handleSubmit()
				if m.userInput.Textarea.Value() != "" {
					t.Fatal("blocked input not reset")
				}
			}
			if *calls != 1 {
				t.Fatalf("hook calls = %d", *calls)
			}
			if len(m.conv.Messages) != 1 || !strings.Contains(m.conv.Messages[0].Content, "Prompt blocked: test policy") {
				t.Fatalf("block notice = %+v", m.conv.Messages)
			}
			if m.services.Reminder.Prepared() || !strings.Contains(strings.Join(m.services.Reminder.Pending(), ""), "context from /clear") {
				t.Fatal("blocked context was drained or lost")
			}
		})
	}
}

func TestPromptAdmissionAcceptedOnceThroughDispatch(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "idle queue"}[queued], func(t *testing.T) {
			m, provider := compactQueueModel(t)
			calls := 0
			m.services.Hook.AddSessionFunctionHook(hook.UserPromptSubmit, "", hook.FunctionHook{
				Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
					calls++
					if in.Prompt != "accepted prompt" {
						t.Fatalf("prompt = %q", in.Prompt)
					}
					return hook.HookOutput{SystemMessage: "admission context"}, nil
				},
			})
			if queued {
				m.userInput.Queue.Enqueue("accepted prompt", nil)
				runCmd(m.sendNextQueuedInputIfIdle())
			} else {
				m.userInput.Textarea.SetValue("accepted prompt")
				runCmd(m.handleSubmit())
			}
			if calls != 1 {
				t.Fatalf("hook calls = %d", calls)
			}
			select {
			case chain := <-provider.requests:
				found := false
				for _, msg := range chain {
					found = found || (strings.Contains(msg.Text(), "accepted prompt") && strings.Contains(msg.Text(), "admission context"))
				}
				if !found {
					t.Fatal("prompt and additional context not delivered together")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no provider request")
			}
		})
	}
}

func TestPromptAdmissionDoesNotGateDeliveryPaths(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "already admitted", true: "midstream queue"}[queued], func(t *testing.T) {
			m, provider := textOnlyModel(t)
			calls := installBlockingPromptHook(t, m)
			if queued {
				m.userInput.Queue.Enqueue("/clear", nil)
				cmd, released := m.releaseQueuedMessage()
				if !released {
					t.Fatal("queue was blocked")
				}
				runCmd(cmd)
			} else {
				runCmd(m.sendToAgent(pendingMessage{Message: core.UserMessage("/clear", nil), FromUser: true}))
			}
			if *calls != 0 {
				t.Fatalf("delivery invoked admission %d times", *calls)
			}
			select {
			case chain := <-provider.requests:
				found := false
				for _, msg := range chain {
					found = found || strings.Contains(msg.Text(), "/clear")
				}
				if !found {
					t.Fatal("slash text did not reach provider stub")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no provider request")
			}
		})
	}
}
