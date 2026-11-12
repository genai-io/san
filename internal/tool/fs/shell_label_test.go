package fs

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestShellLabelExecutionMetadata(t *testing.T) {
	for _, makeTool := range []struct {
		name string
		tool func(*testing.T) *ShellTool
	}{{"bash", bashTool}, {"zsh", zshTool}} {
		t.Run(makeTool.name, func(t *testing.T) {
			sh := makeTool.tool(t)
			cwd := t.TempDir()
			request, err := sh.PreparePermission(context.Background(), map[string]any{"command": "true"}, cwd)
			if err != nil || request.BashMeta.ShellLabel != makeTool.name || request.ToolName != tool.ToolBash {
				t.Fatalf("permission=%+v err=%v", request, err)
			}
			for _, tc := range []struct {
				name    string
				params  map[string]any
				success bool
			}{
				{"silent", map[string]any{"command": "true"}, true},
				{"failure", map[string]any{"command": "exit 7"}, false},
				{"timeout", map[string]any{"command": "sleep 5", "timeout_ms": 10}, false},
				{"invalid", map[string]any{}, false},
				{"background", map[string]any{"command": "true", "execution_mode": shellBackgroundJob}, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					manager := task.NewManager()
					defer func() { manager.Cancel(); manager.Join() }()
					result := sh.ExecuteApproved(task.WithManager(context.Background(), manager), tc.params, cwd)
					details, ok := result.Details.(toolresult.BashDetails)
					if result.Success != tc.success || !ok || details.ShellLabel != makeTool.name || result.Metadata.Title != tool.ToolBash {
						t.Fatalf("result=%+v details=%+v", result, details)
					}
				})
			}
			missing := &ShellTool{shell: proc.Shell{Kind: sh.shell.Kind, Path: filepath.Join(cwd, "missing-shell")}}
			for _, mode := range []string{shellForeground, shellBackgroundJob} {
				result := missing.ExecuteApproved(context.Background(), map[string]any{"command": "true", "execution_mode": mode}, cwd)
				details, ok := result.Details.(toolresult.BashDetails)
				if result.Success || !ok || details.ShellLabel != makeTool.name || details.Error == "" {
					t.Fatalf("start failure=%+v details=%+v", result, details)
				}
			}
		})
	}
}

func TestPowerShellLabelPermissionAndLaunchFailure(t *testing.T) {
	for _, tc := range []struct{ executable, label string }{{"pwsh", "pwsh"}, {"powershell.exe", "PS"}} {
		t.Run(tc.label, func(t *testing.T) {
			cwd := t.TempDir()
			sh := &ShellTool{shell: proc.Shell{Kind: proc.ShellPowerShell, Path: filepath.Join(cwd, tc.executable)}}
			params := map[string]any{"command": "Write-Output hello"}
			req, err := sh.PreparePermission(context.Background(), params, cwd)
			if err != nil || req.ToolName != tool.ToolPowerShell || req.BashMeta.ShellLabel != tc.label {
				t.Fatalf("request=%+v err=%v", req, err)
			}
			for _, mode := range []string{shellForeground, shellBackgroundJob} {
				params["execution_mode"] = mode
				result := sh.ExecuteApproved(context.Background(), params, cwd)
				details, ok := result.Details.(toolresult.BashDetails)
				if result.Success || !ok || details.ShellLabel != tc.label || result.Metadata.Title != tool.ToolPowerShell {
					t.Fatalf("result=%+v details=%+v", result, details)
				}
			}
		})
	}
}

func TestShellLabelSDKPartial(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		rows          int
	}{
		{"silent", "sleep 0.05", 0},
		{"output", "printf 'hello\\n'; sleep 0.2", 1},
	} {
		t.Run(tc.name, func(t *testing.T) { testShellLabelSDKPartial(t, tc.command, tc.rows) })
	}
}

func testShellLabelSDKPartial(t *testing.T, command string, rows int) {
	t.Helper()
	sh := zshTool(t)
	input, _ := json.Marshal(map[string]any{"command": command, "timeout_ms": 1000})
	adapted := tool.AdaptTool(sh, core.ToolSchema{Name: tool.ToolBash, Description: "not interpreter metadata"}, func() string { return t.TempDir() })
	driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "silent", Name: tool.ToolBash, Input: string(input)}), aitest.Says("done"))
	a, err := sdkagent.New(driver.Client(), sdkagent.WithTools(adapted))
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	for event, err := range a.Run(context.Background(), ai.UserMessage("run")) {
		if err != nil {
			t.Fatal(err)
		}
		switch e := event.(type) {
		case sdkagent.ToolUpdate:
			details, ok := e.Partial.Details.(toolresult.BashDetails)
			if !ok || details.ShellLabel != "zsh" {
				t.Fatalf("partial=%+v", e.Partial)
			}
			updates++
		case sdkagent.ToolEnd:
			if e.Err != nil {
				t.Fatal(e.Err)
			}
			if details := e.Result.Details.(toolresult.BashDetails); details.ShellLabel != "zsh" || details.LineCount != rows {
				t.Fatalf("final=%+v", details)
			}
		}
	}
	if updates == 0 {
		t.Fatal("silent command did not emit interpreter metadata")
	}
}
