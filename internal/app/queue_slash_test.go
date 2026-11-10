package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/skill"
)

func TestQueuedSlashCommandsWaitForTurnBoundary(t *testing.T) {
	for _, tc := range []struct {
		raw, notice string
		overlay     bool
	}{
		{raw: "/help", notice: "Available Commands:"},
		{raw: "/settings", overlay: true},
		{raw: "/compact", notice: input.NoProviderMsg},
		{raw: "/not-a-command", notice: "Unknown command: /not-a-command"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.services.Command = command.NewRegistry(command.Options{CWD: m.env.CWD})
			m.conv.Stream.Active = true
			m.userInput.Textarea.SetValue(tc.raw)
			m.handleSubmit()
			m.userInput.Textarea.SetValue("unrelated draft [Image #2]")
			draftImages := []input.PendingImage{{ID: 2, Data: chartImage()}}
			m.userInput.Images.Pending = draftImages
			m.userInput.Images.NextID = 2
			before := len(m.conv.Messages)

			if _, released := m.releaseQueuedMessage(); released || m.userInput.Queue.Len() != 1 {
				t.Fatal("slash command left the queue before the turn ended")
			}
			if len(m.conv.Messages) != before || len(m.userInput.History.Items) != 0 {
				t.Fatal("deferred command changed conversation or input history")
			}

			m.conv.Stream.Stop()
			m.drainTurnQueues()
			if m.userInput.Queue.Len() != 0 {
				t.Fatal("turn boundary did not dispatch the queued command")
			}
			if _, active := m.activeOverlay(); active != tc.overlay {
				t.Fatalf("overlay active = %v, want %v", active, tc.overlay)
			}
			if tc.notice != "" && !strings.Contains(m.conv.Messages[len(m.conv.Messages)-1].Content, tc.notice) {
				t.Fatalf("command notice missing: %+v", m.conv.Messages[before:])
			}
			if got := m.userInput.History.Items; !reflect.DeepEqual(got, []string{tc.raw}) {
				t.Fatalf("input history = %v, want the command once", got)
			}
			if m.userInput.Textarea.Value() != "unrelated draft [Image #2]" || m.userInput.Images.NextID != 2 || !reflect.DeepEqual(m.userInput.Images.Pending, draftImages) {
				t.Fatal("command dispatch changed the composer draft or its images")
			}
			if m.pendingMessages.Len() != 0 {
				t.Fatal("local command was queued for the agent")
			}
			select {
			case <-provider.requests:
				t.Fatal("local command reached the provider")
			default:
			}
		})
	}
}

func TestQueuedLocalCommandsDoNotStrandSuccessor(t *testing.T) {
	for _, boundary := range []string{"turn", "idle", "overlay"} {
		t.Run(boundary, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.services.Command = command.NewRegistry(command.Options{CWD: m.env.CWD})
			if boundary == "overlay" {
				m.userInput.Queue.Enqueue("/settings", nil)
			} else {
				m.userInput.Queue.Enqueue("/help", nil)
				m.userInput.Queue.Enqueue("/not-a-command", nil)
			}
			m.userInput.Queue.Enqueue("first follow-up", nil)
			m.userInput.Queue.Enqueue("second follow-up", nil)
			m.userInput.Textarea.SetValue("unrelated draft")

			if boundary == "idle" {
				m.sendNextQueuedInputIfIdle()
			} else if _, waiting := m.drainTurnQueues(); !waiting {
				t.Fatal("drain did not report the successor turn or overlay")
			}
			if boundary == "overlay" {
				if m.userInput.Queue.Len() != 2 || m.pendingMessages.Len() != 0 {
					t.Fatal("queue continued through the command's overlay")
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			}
			select {
			case request := <-provider.requests:
				if text := request[len(request)-1].Text(); !strings.Contains(text, "first follow-up") {
					t.Fatalf("wrong successor reached the provider: %q", text)
				}
				for _, message := range request {
					if strings.Contains(message.Text(), "/not-a-command") || strings.Contains(message.Text(), "second follow-up") {
						t.Fatal("local command or extra queued message reached the provider")
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatal("local command stranded the queued successor")
			}
			if head, ok := m.userInput.Queue.At(0); !ok || head.Content != "second follow-up" || m.userInput.Queue.Len() != 1 {
				t.Fatal("drain did not preserve one-message FIFO delivery")
			}
			if m.userInput.Textarea.Value() != "unrelated draft" {
				t.Fatal("queue drain changed the composer draft")
			}
		})
	}
}

func TestQueuedLocalCommandAllowsIdleCompletion(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.services.Command = command.NewRegistry(command.Options{CWD: m.env.CWD})
	m.userInput.Queue.Enqueue("/help", nil)
	m.OnTurnEnd(core.Result{StopReason: core.StopEndTurn})
	if m.pendingStopHook == nil || m.userInput.Queue.Len() != 0 {
		t.Fatal("local command suppressed normal idle completion")
	}
}

func TestQueuedSlashCommandRespectsPromptAdmission(t *testing.T) {
	m, provider := compactQueueModel(t)
	calls := installBlockingPromptHook(t, m)
	before := len(m.conv.Messages)
	m.userInput.Queue.Enqueue("/clear", nil)
	if _, waiting := m.drainTurnQueues(); waiting {
		t.Fatal("blocked command suppressed idle completion")
	}
	if *calls != 1 || len(m.conv.Messages) != before+1 || !strings.Contains(m.conv.Messages[before].Content, "Prompt blocked: test policy") {
		t.Fatal("queued command bypassed prompt admission")
	}
	if m.pendingMessages.Len() != 0 || m.userInput.Queue.Len() != 0 {
		t.Fatal("blocked command remained queued for delivery")
	}
	select {
	case <-provider.requests:
		t.Fatal("blocked command reached the provider")
	default:
	}
}

func TestQueuedAbsoluteImagePathIsNotASlashCommand(t *testing.T) {
	m, provider := compactQueueModel(t)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.env.CWD, "queued.png")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	m.conv.Stream.Active = true
	m.userInput.Queue.Enqueue(path+" explain this", nil)
	if _, released := m.releaseQueuedMessage(); !released {
		t.Fatal("leading image path was deferred as a slash command")
	}
	select {
	case request := <-provider.requests:
		text := request[len(request)-1].Text()
		if !strings.Contains(text, path) || !strings.Contains(text, "explain this") {
			t.Fatalf("queued image was not delivered: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued image never reached the provider")
	}
}

func TestQueuedSlashInvocationExpandsInstructions(t *testing.T) {
	for _, kind := range []string{"skill", "custom command"} {
		t.Run(kind, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			path := filepath.Join(m.env.CWD, ".san", "commands", "review.md")
			if kind == "skill" {
				path = filepath.Join(m.env.CWD, ".san", "skills", "review", "SKILL.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("---\nname: review\ndescription: Review queued input\n---\nCheck pending command routing carefully.\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			m.services.Command = command.NewRegistry(command.Options{CWD: m.env.CWD})
			m.services.Skill = skill.Load(skill.Options{CWD: m.env.CWD})
			m.userInput.Queue.Enqueue("/review inspect pending", nil)

			if _, released := m.drainTurnQueues(); !released {
				t.Fatal("queued invocation was not dispatched")
			}
			select {
			case request := <-provider.requests:
				text := request[len(request)-1].Text()
				if !strings.Contains(text, "Check pending command routing carefully.") || !strings.Contains(text, "/review inspect pending") {
					t.Fatalf("invocation was not expanded: %q", text)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("expanded invocation never reached the provider")
			}
			if got := m.conv.Messages[len(m.conv.Messages)-1].DisplayContent; got != "/review inspect pending" {
				t.Fatalf("invocation display = %q", got)
			}
		})
	}
}
