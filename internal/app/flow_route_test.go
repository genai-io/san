package app

import (
	"context"
	"io"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type flowRouteObservation struct {
	msg                         tea.Msg
	active, pending, generating bool
	rendered                    string
}

type flowRouteSessionMsg struct{ owner *session.Session }

type flowRouteModel struct {
	root     *model
	observed chan flowRouteObservation
}

func (m *flowRouteModel) Init() tea.Cmd  { return nil }
func (m *flowRouteModel) View() tea.View { return tea.NewView("") }
func (m *flowRouteModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if change, ok := msg.(flowRouteSessionMsg); ok {
		m.root.services.Session = change.owner
		m.root.services.sessionID = change.owner.ID()
	}
	_, cmd := m.root.Update(msg)
	switch msg.(type) {
	case initialPromptMsg, input.FlowDraftRequestMsg, flowDraftedMsg, tea.KeyPressMsg, flowRouteSessionMsg:
		pending := m.root.flowDraft
		m.observed <- flowRouteObservation{msg: msg, active: m.root.userInput.Flow.IsActive(), pending: pending != nil,
			generating: pending != nil && m.root.userInput.Flow.IsGenerating(pending.generation), rendered: m.root.userInput.Flow.Render()}
	}
	return m, cmd
}

func TestFlowDraftSlashUpdateRoute(t *testing.T) {
	for _, scenario := range []string{"success", "blocked-release", "blocked-escape", "blocked-session-switch"} {
		t.Run(scenario, func(t *testing.T) {
			m := draftTestModel(t)
			originalOwner := m.services.Session
			m.services.Flow = flow.NewService(m.services.Task)
			m.env.CWD = t.TempDir()
			m.conv.Messages = nil
			var calls atomic.Int32
			started := make(chan struct{}, 2)
			release := make(chan struct{})
			turn := func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				call := calls.Add(1)
				if len(req.Tools) != 0 {
					t.Error("draft advertised tools")
				}
				if !strings.HasSuffix(req.Messages[len(req.Messages)-1].Content.Text(), "Inspect this repository") {
					t.Error("slash goal changed")
				}
				started <- struct{}{}
				if call == 1 && scenario != "success" {
					select {
					case <-release:
					case <-ctx.Done():
						return func(yield func(ai.Delta, error) bool) { yield(ai.Delta{}, ctx.Err()) }
					}
				}
				return aitest.Says(draftBlueprint)(ctx, req)
			}
			driver := aitest.New(turn, turn)
			m.env.LLMProvider = &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			wrapped := &flowRouteModel{root: m, observed: make(chan flowRouteObservation, 32)}
			program := tea.NewProgram(wrapped, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(100, 35))
			done := make(chan struct{})
			var runErr error
			go func() { _, runErr = program.Run(); close(done) }()
			t.Cleanup(func() {
				program.Kill()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("program did not stop")
				}
			})
			next := func() flowRouteObservation {
				t.Helper()
				select {
				case observation := <-wrapped.observed:
					return observation
				case <-done:
					t.Fatalf("program exited: %v", runErr)
				case <-time.After(5 * time.Second):
					t.Fatal("flow route did not advance")
				}
				return flowRouteObservation{}
			}
			submit := func() {
				t.Helper()
				program.Send(initialPromptMsg("/flow Inspect this repository"))
				opened := next()
				if _, ok := opened.msg.(initialPromptMsg); !ok || !opened.active {
					t.Fatalf("slash did not open generation: %+v", opened)
				}
				requested := next()
				if _, ok := requested.msg.(input.FlowDraftRequestMsg); !ok || !requested.pending || !requested.generating {
					t.Fatalf("request did not reach host: %+v", requested)
				}
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("returned completion command was not executed")
				}
			}
			submit()
			if strings.HasPrefix(scenario, "blocked") {
				select {
				case got := <-wrapped.observed:
					t.Fatalf("blocked provider unexpectedly settled: %+v", got)
				default:
				}
				switch scenario {
				case "blocked-release":
					close(release)
				case "blocked-escape":
					program.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
				case "blocked-session-switch":
					store, err := session.OpenStore(t.Context(), t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { store.Close() })
					owner, err := store.Create(t.Context(), session.CreateOptions{ID: "replacement"})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { owner.Close() })
					program.Send(flowRouteSessionMsg{owner})
				}
				if scenario != "blocked-release" {
					canceled := next()
					if canceled.active || canceled.pending {
						t.Fatalf("cancellation did not clear ownership: %+v", canceled)
					}
					stale := next()
					if _, ok := stale.msg.(flowDraftedMsg); !ok || stale.active || stale.pending {
						t.Fatalf("canceled completion revived draft: %+v", stale)
					}
					submit()
				}
			}
			settled := next()
			if _, ok := settled.msg.(flowDraftedMsg); !ok || settled.pending || !settled.active || !strings.Contains(strings.ToLower(settled.rendered), "review") {
				t.Fatalf("completion did not reach unsaved Review: %+v", settled)
			}
			wantCalls := int32(1)
			if scenario == "blocked-escape" || scenario == "blocked-session-switch" {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Fatalf("provider calls = %d, want %d", calls.Load(), wantCalls)
			}
			program.Quit()
			select {
			case <-done:
				if runErr != nil {
					t.Fatal(runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("program did not quit")
			}
			if len(m.services.Flow.SavedDefinitions()) != 0 || len(m.services.Agent.Messages()) != 0 || m.conv.Stream.Active {
				t.Fatal("draft was saved, run, or added to the main conversation")
			}
			for _, owner := range []*session.Session{originalOwner, m.services.Session} {
				projection := owner.Snapshot()
				if len(projection.Unfinished().Inferences) != 0 {
					t.Fatal("draft recording left unfinished")
				}
			}
		})
	}
}
