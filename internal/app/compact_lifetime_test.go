package app

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/chat"
)

type compactHTTPProvider struct {
	restartStubProvider
	client *ai.Client
}

func (p *compactHTTPProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}

func waitCompact(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not reach expected boundary")
	}
}

// Exercise the full app request -> shared summarizer -> SDK -> net/http path.
// The server has accepted the request but sends no headers before cancellation.
func TestManualCompactionCancelsHTTP(t *testing.T) {
	for _, action := range []string{"escape", "ctrl-c", "reset", "stop", "replace", "quit"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			m.userInput = input.New(m.env.CWD, 80, nil, input.SelectorDeps{Setting: m.services.Setting})
			entered, aborted := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-r.Context().Done()
				close(aborted)
			}))
			defer func() { server.CloseClientConnections(); server.Close() }()
			modelInfo := ai.Model{ID: "m", BaseURL: server.URL + "/"}
			driver, err := chat.New(ai.Config{Model: modelInfo, HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, modelInfo)}
			request := m.BuildCompactRequest("", "manual")
			m.OnCompactStart(len(request.Messages))
			result := make(chan conv.CompactResultMsg, 1)
			go func() { result <- conv.CompactCmd(request)().(conv.CompactResultMsg) }()
			waitCompact(t, entered)
			switch action {
			case "escape":
				m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
			case "ctrl-c":
				m.routeKeypress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			case "reset":
				if err := m.ResetAgentRuntime(); err != nil {
					t.Fatal(err)
				}
			case "stop":
				if err := m.StopAgentRuntime(); err != nil {
					t.Fatal(err)
				}
			case "replace":
				opened, err := store.Create(session.LegacyCreateOptions{ID: "replacement"})
				if err != nil {
					t.Fatal(err)
				}
				m.replaceSession(opened)
			case "quit":
				m.QuitWithCancel()
			}
			waitCompact(t, aborted)
			if !errors.Is(request.Ctx.Err(), context.Canceled) || m.pendingCompaction != nil || m.conv.Compact.Active {
				t.Fatal("request lifetime survived cancellation")
			}
			before := m.services.Agent.Messages()
			select {
			case msg := <-result:
				if msg.Err == nil {
					t.Fatal("cancelled HTTP returned success")
				}
				if cmd := m.HandleCompactResult(msg); cmd != nil {
					t.Fatal("cancelled result produced effects")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("summary call did not unwind")
			}
			if !reflect.DeepEqual(before, m.services.Agent.Messages()) {
				t.Fatal("cancelled summary changed history")
			}
		})
	}
}

func TestManualCompactionFencesLateResults(t *testing.T) {
	for _, change := range []string{"reset", "replace", "same-session-writer", "runtime", "successor", "no-identity"} {
		for _, failed := range []bool{false, true} {
			t.Run(change+map[bool]string{true: "/error", false: "/success"}[failed], func(t *testing.T) {
				m, store := resumedModel(t, parallelResultChain())
				request := m.BuildCompactRequest("", "manual")
				msg := conv.CompactResultMsg{Context: request.Ctx, CompactResult: agent.CompactResult{Summary: "stale summary", OriginalCount: len(request.Messages)}}
				if failed {
					msg.Err = errors.New("stale error")
				}
				switch change {
				case "reset":
					if err := m.ResetAgentRuntime(); err != nil {
						t.Fatal(err)
					}
				case "replace":
					opened, err := store.Create(session.LegacyCreateOptions{ID: "replacement"})
					if err != nil {
						t.Fatal(err)
					}
					m.replaceSession(opened)
				case "same-session-writer":
					if err := m.services.Session.Close(); err != nil {
						t.Fatal(err)
					}
					opened, _, err := store.Open("s")
					if err != nil {
						t.Fatal(err)
					}
					m.replaceSession(opened)
				case "runtime":
					provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
					if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m"}, m.services.Agent.Messages()); err != nil {
						t.Fatal(err)
					}
				case "successor":
					m.BuildCompactRequest("new focus", "manual")
				case "no-identity":
					msg.Context = nil
				}
				m.OnCompactStart(99)
				pending := m.pendingCompaction
				before := m.services.Agent.Messages()
				history, err := store.Read(m.sessionID(), session.LegacyReadOptions{FullHistory: true})
				if err != nil {
					t.Fatal(err)
				}
				rows := store.ChatRows(history)
				if cmd := m.HandleCompactResult(msg); cmd != nil {
					t.Fatal("stale result produced command")
				}
				afterHistory, err := store.Read(m.sessionID(), session.LegacyReadOptions{FullHistory: true})
				if err != nil {
					t.Fatal(err)
				}
				afterRows := store.ChatRows(afterHistory)
				if m.pendingCompaction != pending || !m.conv.Compact.Active || m.conv.Compact.LastResult != "" || m.env.Compressions != 0 || !reflect.DeepEqual(before, m.services.Agent.Messages()) || !reflect.DeepEqual(rows, afterRows) {
					t.Fatal("stale result altered current state or recording")
				}
			})
		}
	}
}

func TestManualCompactionCancelPreservesInputPrecedence(t *testing.T) {
	m, _ := compactQueueModel(t)
	request := m.BuildCompactRequest("", "manual")
	m.OnCompactStart(len(request.Messages))
	m.userInput.Textarea.SetValue("draft")
	m.routeKeypress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if request.Ctx.Err() != nil || m.userInput.Textarea.Value() != "" {
		t.Fatal("Ctrl+C did not clear draft before cancelling")
	}
	m.userInput.PromptSuggestion.Text = "offer"
	m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if request.Ctx.Err() != nil || m.userInput.PromptSuggestion.Text != "" {
		t.Fatal("Escape did not dismiss offer before cancelling")
	}
	m.userInput.Textarea.SetValue("queued continuation")
	m.handleSubmit()
	m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if request.Ctx.Err() == nil || !m.pendingMessages.Held() || m.userInput.Queue.Len() != 1 || !m.services.Agent.Active() {
		t.Fatal("cancel did not hold input independently of runtime")
	}
	if cmd := m.sendNextQueuedInputIfIdle(); cmd != nil {
		t.Fatal("cancel automatically resumed queued work")
	}
	m.userInput.Textarea.SetValue("explicit continuation")
	m.handleSubmit()
	if m.pendingMessages.Held() {
		t.Fatal("explicit input did not release cancellation hold")
	}
}

func TestManualCompactionCompletedResultOwnership(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "late-success"}[replace], func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			entered, release := make(chan struct{}), make(chan struct{})
			driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				close(entered)
				<-release
				return aitest.Says("controlled summary")(ctx, req)
			})
			m.env.LLMProvider = &compactHTTPProvider{client: driver.Client()}
			request := m.BuildCompactRequest("focus snapshot", "manual")
			m.OnCompactStart(len(request.Messages))
			result := make(chan conv.CompactResultMsg, 1)
			go func() { result <- conv.CompactCmd(request)().(conv.CompactResultMsg) }()
			waitCompact(t, entered)
			// The command must not re-read mutable model/provider selection.
			m.env.LLMProvider = nil
			close(release)
			var msg conv.CompactResultMsg
			select {
			case msg = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("summary did not finish")
			}
			if msg.Err != nil {
				t.Fatal(msg.Err)
			}
			if replace {
				if err := m.ResetAgentRuntime(); err != nil {
					t.Fatal(err)
				}
			}
			m.HandleCompactResult(msg)
			if m.pendingCompaction != nil || request.Ctx.Err() == nil {
				t.Fatal("completed request retained ownership")
			}
			loaded, err := store.Read("s", session.LegacyReadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if replace {
				if len(m.services.Agent.Messages()) != 0 || len(loaded.Messages) != len(request.Messages) {
					t.Fatal("late successful result changed history")
				}
			} else if len(loaded.Messages) != 1 || !reflect.DeepEqual(loaded.Messages, m.services.Agent.Messages()) || m.conv.Compact.Active {
				t.Fatal("successful result did not record and replace the native chain")
			}
		})
	}
}

func TestManualCompactionRequestFailureReleasesOwnership(t *testing.T) {
	m, _ := resumedModel(t, parallelResultChain())
	m.env.LLMProvider = &failedTurnProvider{}
	request := m.BuildCompactRequest("", "manual")
	m.OnCompactStart(len(request.Messages))
	msg := conv.CompactCmd(request)().(conv.CompactResultMsg)
	if msg.Err == nil {
		t.Fatal("expected provider failure")
	}
	m.HandleCompactResult(msg)
	if m.pendingCompaction != nil || request.Ctx.Err() == nil || m.conv.Compact.Active || !m.conv.Compact.LastError {
		t.Fatal("failure retained ownership or progress UI")
	}
	m.services.Agent.SetMessages(nil)
	m.BuildCompactRequest("", "manual")
	if m.pendingCompaction != nil {
		t.Fatal("rejected short request acquired a lifetime")
	}
}

func TestManualCompactionOpensWriterBeforeReplacingHistory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "open-fails"}[fail], func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			if err := m.services.Session.Close(); err != nil {
				t.Fatal(err)
			}
			m.services.Session = nil
			m.services.sessionID = "unopened"
			if fail {
				// An already leased writer cannot be replaced by a new Create.
				opened, err := store.Create(session.LegacyCreateOptions{ID: "unopened"})
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
			}
			m.env.LLMProvider = &compactHTTPProvider{client: aitest.New(aitest.Says("summary")).Client()}
			request := m.BuildCompactRequest("", "manual")
			m.OnCompactStart(len(request.Messages))
			m.HandleCompactResult(conv.CompactCmd(request)().(conv.CompactResultMsg))
			if m.pendingCompaction != nil || m.conv.Compact.Active {
				t.Fatal("result retained request/progress")
			}
			if fail {
				if !m.conv.Compact.LastError || !reflect.DeepEqual(m.services.Agent.Messages(), request.Messages) {
					t.Fatal("failed recording replaced history")
				}
			} else {
				loaded, err := store.Read("unopened", session.LegacyReadOptions{})
				if err != nil || len(loaded.Messages) != 1 || !reflect.DeepEqual(loaded.Messages, m.services.Agent.Messages()) {
					t.Fatalf("unopened summary was not recorded: %v", err)
				}
			}
		})
	}
}

func TestManualCompactionCancelsPreCompactHook(t *testing.T) {
	m, _ := resumedModel(t, parallelResultChain())
	entered, cancelled := make(chan struct{}), make(chan struct{})
	m.services.Hook.AddSessionFunctionHook(hook.PreCompact, "", hook.FunctionHook{Callback: func(ctx context.Context, _ hook.HookInput) (hook.HookOutput, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return hook.HookOutput{}, ctx.Err()
	}})
	request := m.BuildCompactRequest("", "manual")
	result := make(chan agent.CompactResult, 1)
	go func() { result <- request.Run() }()
	waitCompact(t, entered)
	m.cancelManualCompaction()
	waitCompact(t, cancelled)
	select {
	case got := <-result:
		if !errors.Is(got.Err, context.Canceled) {
			t.Fatalf("result: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook cancellation did not unwind request")
	}
}
