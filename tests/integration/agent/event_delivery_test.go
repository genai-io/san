package subagent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestAgentRunningShellErrorNotices(t *testing.T) {
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	if shell.Kind != proc.ShellBash {
		t.Skip("regression exercises Bash process-group cancellation")
	}
	for _, cancelRun := range []bool{true, false} {
		name := "tool_timeout"
		if cancelRun {
			name = "worker_cancellation"
		}
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(cwd, "example.go"), []byte("ready\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			started := make(chan struct{})
			var once sync.Once
			ctx = tool.ContextWithBashProgress(ctx, func(_ int, bytes int64) {
				if bytes > 0 {
					once.Do(func() { close(started) })
				}
			})
			input := `{"command":"tail -f example.go","timeout":1000}`
			if cancelRun {
				input = `{"command":"tail -f example.go"}`
			}
			driver := aitest.New(aitest.Asks(ai.ToolCall{
				ID: "tail-source", Name: shell.Kind.ToolName(), Input: input,
			}), aitest.Says("Review finished"))
			executor := subagent.NewExecutor(driverProvider{&testutil.FakeProvider{}, driver}, cwd, "fake-model", nil)
			var notices []broker.Message
			broker.Register(broker.Main, func(message broker.Message) bool {
				notices = append(notices, message)
				return true
			})
			t.Cleanup(func() { broker.Unregister(broker.Main) })
			var toolErr error
			var output string
			var activity []string
			done := make(chan error, 1)
			go func() {
				_, err := executor.Run(ctx, tool.AgentExecRequest{
					Mode: "explore", Description: "Review source", Prompt: "Watch the source",
					OnActivity:   func(line string) { activity = append(activity, line) },
					OnToolResult: func(_, _, text string, err error) { output, toolErr = text, err },
				})
				done <- err
			}()
			select {
			case <-started:
				// Output proves the command is running, not merely queued or gated.
			case <-ctx.Done():
				t.Fatal("shell command never started")
			}
			if cancelRun {
				cancel()
			}
			select {
			case err := <-done:
				if cancelRun && !errors.Is(err, context.Canceled) {
					t.Fatalf("worker did not return cancellation: %v", err)
				}
				if !cancelRun && err != nil {
					t.Fatalf("tool-local timeout ended worker: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("shell error did not release worker")
			}
			if toolErr == nil || !strings.Contains(output, "ready") {
				t.Fatalf("tool-result callback lost partial output or error: %q / %v", output, toolErr)
			}
			if cancelRun {
				if errors.Is(toolErr, context.Canceled) {
					t.Fatal("regression must exercise the legacy adapter's non-context exit error")
				}
				if len(notices) != 0 {
					t.Fatalf("stopping a running command emitted false failures: %+v", notices)
				}
				for _, line := range activity {
					if strings.Contains(line, "failed:") {
						t.Fatalf("cancellation leaked a fallback error notice: %q", line)
					}
				}
			} else if len(notices) != 1 || !strings.Contains(notices[0].Subject, "command timed out") || notices[0].Content != "" {
				t.Fatalf("tool-local timeout must still produce a display-only notice: %+v", notices)
			}
		})
	}
}
