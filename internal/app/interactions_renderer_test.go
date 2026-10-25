package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/ask"
	_ "github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/tests/integration/testutil"
)

// Use the real Update, renderer, modal keyboard handlers, tool adapters and
// worker loop. Only the network provider and unrelated startup services are replaced.
type interactionRendererModel struct{ *model }

func (m *interactionRendererModel) Init() tea.Cmd { return m.conv.AgentToUI.Check() }

func TestWorkerInteractionsThroughMainTUI(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, scenario := range []struct {
			name    string
			mode    subagent.PermissionMode
			approve bool
		}{
			{"default/approve", subagent.PermissionDefault, true},
			{"default/reject", subagent.PermissionDefault, false},
			{"yolo/question", subagent.PermissionBypass, true},
		} {
			mode := scenario.mode
			t.Run(scenario.name+map[bool]string{false: "/foreground", true: "/background"}[background], func(t *testing.T) {
				cwd := t.TempDir()
				marker := filepath.Join(cwd, "ui-approved-marker.txt")
				provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
					testutil.ToolCallResponse(tool.ToolWrite, "write-call", `{"file_path":"ui-approved-marker.txt","content":"approved"}`),
					testutil.ToolCallResponse(tool.ToolAskUserQuestion, "question-call", `{"question":"WORKER-QUESTION: choose the worker answer","options":["first","second"]}`),
					testutil.EndTurnResponse("worker finished"),
				}}
				m := interactionModel()
				terminal := newTerminalHistoryState(80, 24)
				program := tea.NewProgram(&interactionRendererModel{m}, tea.WithInput(nil), tea.WithOutput(terminal),
					tea.WithWindowSize(80, 24), tea.WithEnvironment([]string{"TERM=xterm-256color"}))
				uiDone := make(chan error, 1)
				go func() { _, err := program.Run(); uiDone <- err }()
				bridge := m.conv.AgentToUI
				defer func() {
					bridge.Close()
					program.Quit()
					select {
					case err := <-uiDone:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(3 * time.Second):
						program.Kill()
						t.Error("TUI did not stop")
					}
				}()
				waitFor := func(text string) {
					t.Helper()
					deadline := time.Now().Add(3 * time.Second)
					for {
						_, screen := terminal.snapshot()
						if strings.Contains(screen, text) {
							return
						}
						if time.Now().After(deadline) {
							t.Fatalf("TUI did not display %q:\n%s", text, screen)
						}
						time.Sleep(time.Millisecond)
					}
				}
				executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
				executor.SetParentPermissionMode(func() subagent.PermissionMode { return mode })
				executor.SetInteractions(bridge)
				req := tool.AgentExecRequest{Prompt: "Write a marker, ask a question, then finish", ParentToolCallID: "spawning-call"}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var backgroundTask *task.AgentTask
				workerDone := make(chan error, 1)
				if background {
					var err error
					backgroundTask, err = executor.RunBackground(req)
					if err != nil {
						t.Fatal(err)
					}
					defer backgroundTask.Stop()
				} else {
					go func() { _, err := executor.Run(ctx, req); workerDone <- err }()
				}
				if mode == subagent.PermissionDefault {
					waitFor("1. Allow once")
					waitFor("2. Deny")
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("worker executed Write before confirmation")
					}
					_, screen := terminal.snapshot()
					if strings.Contains(screen, "More permissions") || strings.Contains(screen, "for this session") {
						t.Fatal("worker UI offered a parent-scoped grant")
					}
					key := tea.KeyEnter
					if !scenario.approve {
						key = tea.KeyEscape
					}
					program.Send(tea.KeyPressMsg{Code: key})
				}
				waitFor("WORKER-QUESTION")
				_, statErr := os.Stat(marker)
				if scenario.approve && statErr != nil {
					t.Fatalf("approved Write did not run: %v", statErr)
				}
				if !scenario.approve && !os.IsNotExist(statErr) {
					t.Fatal("rejected Write executed")
				}
				program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
				if background {
					if !backgroundTask.WaitForCompletion(3 * time.Second) {
						t.Fatal("background worker did not resume after its answer")
					}
				} else {
					select {
					case err := <-workerDone:
						if err != nil {
							t.Fatal(err)
						}
					case <-ctx.Done():
						t.Fatal("foreground worker did not resume after its answer")
					}
				}
				if len(provider.Calls) != 3 {
					t.Fatalf("worker did not finish its actual tool loop: %d calls", len(provider.Calls))
				}
				found := false
				for _, message := range provider.Calls[2].Messages {
					for _, result := range message.ToolResults() {
						if result.ToolCallID == "question-call" && !result.IsError && strings.Contains(result.Content.Text(), "first") {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("UI answer did not return to the worker's next inference")
				}
			})
		}
	}
}
