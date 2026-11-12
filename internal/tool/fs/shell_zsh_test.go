package fs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func zshTool(t *testing.T) *ShellTool {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native zsh is supported on Unix")
	}
	path, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	return &ShellTool{shell: proc.Shell{Kind: proc.ShellZsh, Path: path}}
}

func TestZshTracksDirectoryAndExitStatus(t *testing.T) {
	shell := zshTool(t)
	cwd := t.TempDir()
	child := filepath.Join(cwd, "child dir")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, exit := range []int{0, 7} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			result := shell.ExecuteApproved(context.Background(), map[string]any{
				"command": fmt.Sprintf("cd 'child dir'; [[ -n $ZSH_VERSION ]] || exit 99; printf 'zsh\\n'; exit %d", exit),
			}, cwd)
			if result.Success != (exit == 0) || result.Output != "zsh\n" {
				t.Fatalf("unexpected zsh result: %+v", result)
			}
			if exit != 0 && result.Error != "exit code 7" {
				t.Fatalf("exit error = %q", result.Error)
			}
			got, err := filepath.EvalSymlinks(result.HookResponse.(map[string]any)["cwd"].(string))
			want, wantErr := filepath.EvalSymlinks(child)
			if err != nil || wantErr != nil || got != want {
				t.Fatalf("tracked cwd = %q, want %q (%v, %v)", got, want, err, wantErr)
			}
		})
	}
}

func TestZshSkipsUserStartupAndPreservesEnvironment(t *testing.T) {
	shell := zshTool(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".zshenv"), []byte("export SAN_ZSH_STARTUP=loaded\nprintf 'unexpected startup output\\n'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithShellEnv(context.Background(), []string{"ZDOTDIR=" + dir, "SAN_ZSH_STARTUP=unset", "SAN_ZSH_INHERITED=kept"})
	result := shell.ExecuteApproved(ctx, map[string]any{
		"command": `printf '%s:%s\n' "${SAN_ZSH_STARTUP-unset}" "$SAN_ZSH_INHERITED"`,
	}, dir)
	if !result.Success || result.Output != "unset:kept\n" {
		t.Fatalf("startup/environment contract changed: %+v", result)
	}
}

func TestBackgroundZshUsesSelectedInterpreter(t *testing.T) {
	shell := zshTool(t)
	manager := task.NewManager()
	t.Cleanup(func() { manager.Cancel(); manager.Join() })
	ctx := task.WithManager(context.Background(), manager)
	result := shell.ExecuteApproved(ctx, map[string]any{
		"command":        `[[ -n $ZSH_VERSION ]] || exit 99; printf 'zsh-background\n'`,
		"execution_mode": shellBackgroundJob,
	}, t.TempDir())
	if !result.Success {
		t.Fatal(result.Error)
	}
	id := result.Details.(toolresult.BashDetails).TaskID
	job, ok := manager.Get(id)
	if !ok || !job.WaitForCompletion(5*time.Second) {
		t.Fatal("background zsh did not finish")
	}
	if info := job.GetStatus(); info.Status != task.StatusCompleted || job.GetOutput() != "zsh-background\n" {
		t.Fatalf("background zsh failed: %+v, %q", info, job.GetOutput())
	}
}

func TestZshSchemaKeepsBashIdentityAndNamesInterpreter(t *testing.T) {
	shell := &ShellTool{shell: proc.Shell{Kind: proc.ShellZsh, Path: "/bin/zsh"}}
	schema := shell.Schema()
	if schema.Name != "Bash" || !strings.Contains(schema.Description, "Executes a zsh command") || !strings.Contains(schema.Description, "zsh -f") {
		t.Fatalf("incorrect shell contract: %+v", schema)
	}
	if !strings.Contains(schema.Description, "human confirmation") {
		t.Fatal("native syntax confirmation boundary is missing")
	}
}
