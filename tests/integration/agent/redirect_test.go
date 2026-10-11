package subagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestAgentExploreCannotWriteThroughCompoundRedirect(t *testing.T) {
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	if shell.Kind != proc.ShellBash {
		t.Skip("regression exercises Bash compound-statement redirects")
	}
	for _, command := range []string{
		"{ cat example.go; } > review-copy.go",
		"(cat example.go) >> review-copy.go",
		"{ (cat example.go); } > new-review-copy.go",
	} {
		t.Run(command, func(t *testing.T) {
			cwd := t.TempDir()
			for name, content := range map[string]string{"example.go": "source\n", "review-copy.go": "original\n"} {
				if err := os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			input, err := json.Marshal(map[string]string{"command": command})
			if err != nil {
				t.Fatal(err)
			}
			provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
				testutil.ToolCallResponse(tool.ToolBash, "redirect-source", string(input)),
				testutil.EndTurnResponse("Write blocked"),
			}}
			executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
			result, err := executor.Run(context.Background(), tool.AgentExecRequest{Mode: "explore", Prompt: "Review the source"})
			if err != nil || !result.Success {
				t.Fatalf("worker did not recover from denial: result=%+v, err=%v", result, err)
			}
			denied := false
			for _, message := range result.Messages {
				for _, tr := range message.ToolResults() {
					denied = denied || (tr.IsError && strings.Contains(tr.Content.Text(), "denied in Explore mode"))
				}
			}
			if !denied {
				t.Fatal("compound write did not reach the explore permission barrier")
			}
			if content, err := os.ReadFile(filepath.Join(cwd, "review-copy.go")); err != nil || string(content) != "original\n" {
				t.Fatalf("explore overwrote or appended to the destination: %q / %v", content, err)
			}
			if _, err := os.Stat(filepath.Join(cwd, "new-review-copy.go")); !os.IsNotExist(err) {
				t.Fatalf("explore created the destination: %v", err)
			}
		})
	}
}
