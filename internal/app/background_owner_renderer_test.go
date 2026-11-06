package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// Only inference is omitted: real Bash, lifecycle routing, notice rendering,
// model.View and Bubble Tea's renderer run against an emulated terminal. The
// foreground Agent call is held in flight deterministically, without a provider.
type ownerRendererModel struct{ *fixedComposerRendererModel }

func (m *ownerRendererModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if n, ok := msg.(mainNotice); ok {
		// Exercise the production display seam without submitting a provider turn.
		m.showNoticeLine(n)
		return m, nil
	}
	_, cmd := m.fixedComposerRendererModel.Update(msg)
	return m, cmd
}

func TestBackgroundOwnerRenderer(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			width, height := size[0], size[1]
			broker.Reset()
			manager := task.Default()
			previous := manager.SessionID()
			manager.SetSessionID("")
			t.Cleanup(func() { task.SetLifecycleHandler(nil); broker.Reset(); manager.SetSessionID(previous) })
			m := &ownerRendererModel{&fixedComposerRendererModel{model: fixedComposerModel(width, height), settled: make(chan struct{}, 16)}}
			m.services.Task = manager
			m.env.AgentPreviewLines = 1
			m.wireTaskLifecycle(nil)
			m.userInput.Textarea.SetValue("COMPOSER-LIVE")
			call := core.ToolCall{ID: "owner-worker", Name: "Agent", Input: `{"description":"live-worker","prompt":"work"}`}
			m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{call}})
			m.conv.Tool.PendingCalls = []core.ToolCall{call}
			m.conv.Tool.MarkStarted(call.ID)
			m.conv.TaskActivity = map[int][]string{0: {"Running owner check"}}
			terminal := newTerminalHistoryState(width, height)
			var writer io.Writer = terminal
			if os.Getenv("SAN_OWNER_TMUX") == "1" {
				writer = io.MultiWriter(terminal, os.Stdout)
			}
			m.flush.output = &terminalOutput{writer: writer}
			startup := &rendererStartupModel{Model: m, ready: make(chan struct{})}
			program := tea.NewProgram(startup, tea.WithInput(nil), tea.WithOutput(m.flush.output), tea.WithEnvironment([]string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}), tea.WithFPS(60), tea.WithWindowSize(width, height))
			done := make(chan error, 1)
			go func() { _, err := program.Run(); done <- err }()
			defer func() {
				program.Quit()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					program.Kill()
					<-done
					t.Error("renderer shutdown timeout")
				}
			}()
			child := make(chan broker.Message, 4)
			main := make(chan broker.Message, 4)
			broker.Register("owner-child", func(msg broker.Message) bool { child <- msg; return true })
			broker.Register(broker.Main, func(msg broker.Message) bool { main <- msg; program.Send(fromBrokerMessage(msg)); return true })
			waitFrame := func(marker string) string {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for {
					history, screen := terminal.snapshot()
					terminal.mu.Lock()
					_, y := terminal.state.Cursor()
					terminal.mu.Unlock()
					if strings.Contains(screen, marker) && strings.Contains(screen, "live-worker") && strings.Contains(screen, "Running owner check") && strings.Contains(screen, "COMPOSER-LIVE") && y == height-3 {
						return history + "\n" + screen
					}
					if time.Now().After(deadline) {
						t.Fatalf("unstable terminal waiting for %q (cursor %d):\n%s", marker, y, screen)
					}
					time.Sleep(time.Millisecond)
				}
			}
			startup.wait(t)
			waitFrame("live-worker")
			launch := func(owner, description, command string) string {
				t.Helper()
				ctx := tool.WithAgentID(context.Background(), owner)
				result := (&fs.ShellTool{}).ExecuteApproved(ctx, map[string]any{"command": command, "description": description, "execution_mode": "background_job"}, t.TempDir())
				if !result.Success {
					t.Fatal(result.Error)
				}
				id := result.Details.(toolresult.BashDetails).TaskID
				t.Cleanup(func() { manager.Remove(id) })
				return id
			}
			for _, tc := range []struct{ description, command, status string }{{"CHILD-OK", "printf child-secret-ok", "completed"}, {"CHILD-FAIL", "printf child-secret-fail; exit 7", "failed"}} {
				id := launch("owner-child", tc.description, tc.command)
				select {
				case msg := <-child:
					if msg.TaskCompletion == nil || msg.TaskCompletion.TaskID != id || !strings.Contains(msg.Content, `status="`+tc.status+`"`) {
						t.Fatalf("wrong child completion: %+v", msg)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("child completion timeout")
				}
				if len(main) != 0 || len(m.services.Tracker.List()) != 0 {
					t.Fatal("child completion reached main route or tracker")
				}
				all := waitFrame("live-worker")
				for _, token := range []string{"CHILD-OK", "CHILD-FAIL", "child-secret", id} {
					if strings.Contains(all, token) {
						t.Fatalf("child notice leaked to terminal: %s", all)
					}
				}
			}
			launch("", "MAIN-DONE", "printf main-output")
			select {
			case <-main:
			case <-time.After(5 * time.Second):
				t.Fatal("main completion timeout")
			}
			all := waitFrame("MAIN-DONE")
			if !strings.Contains(all, "completed") {
				t.Fatalf("main completion status absent: %s", all)
			}
			for _, token := range []string{"CHILD-OK", "CHILD-FAIL", "child-secret"} {
				if strings.Contains(all, token) {
					t.Fatalf("child notice leaked: %s", all)
				}
			}
			if os.Getenv("SAN_OWNER_TMUX") != "1" {
				t.Logf("terminal %dx%d: active worker, main completion, stable composer; child notices absent", width, height)
			}
			// Leave a stable live frame for capture-pane when explicitly requested.
			if os.Getenv("SAN_OWNER_TMUX") == "1" {
				time.Sleep(3 * time.Second)
			}
		})
	}
}
