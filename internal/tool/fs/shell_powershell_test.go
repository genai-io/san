package fs

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/tool"
)

// The command reaches PowerShell byte for byte: -EncodedCommand carries it as
// UTF-16LE, so no quote or backslash is re-read by a command line.
func TestPowerShellArgsCarryTheScriptVerbatim(t *testing.T) {
	script := `Write-Output 'a"b\c' ; "中文"`
	args := powerShellArgs(script)
	raw, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Errorf("decoded %q, want %q", got, script)
	}
}

// A script past the command-line cap runs from a temp file instead, which
// removes itself when PowerShell runs it.
func TestPowerShellArgsMoveALongScriptToAFile(t *testing.T) {
	script := strings.Repeat("#", maxEncodedCommand)
	args := powerShellArgs(script)
	if args[len(args)-2] != "-File" {
		t.Fatalf("args = %q, want -File", args[:len(args)-1])
	}
	path := args[len(args)-1]
	defer os.Remove(path)
	body, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(body), "\n"+script) {
		t.Fatalf("temp script does not end with the command (err %v)", err)
	}
}

func TestShellToolIsNamedForItsShell(t *testing.T) {
	bash := (&ShellTool{shell: proc.Shell{Kind: proc.ShellBash, Path: "/bin/bash"}}).Schema()
	if bash.Name != tool.ToolBash {
		t.Errorf("bash tool named %q", bash.Name)
	}
	ps := (&ShellTool{shell: proc.Shell{Kind: proc.ShellPowerShell, Path: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}}).Schema()
	if ps.Name != tool.ToolPowerShell {
		t.Errorf("PowerShell tool named %q", ps.Name)
	}
	if !strings.Contains(ps.Description, "Windows PowerShell 5.1") || !strings.Contains(ps.Description, "no && or ||") {
		t.Errorf("the PowerShell description does not say which shell it is or what it lacks:\n%s", ps.Description)
	}
}

// eachPowerShell runs fn under every PowerShell this machine has: pwsh and
// Windows PowerShell 5.1, which a stock Windows ships and which differs in
// encoding and operators. Windows CI has both; elsewhere it usually skips.
func eachPowerShell(t *testing.T, fn func(t *testing.T, sh *ShellTool)) {
	t.Helper()
	ran := false
	for _, name := range []string{"pwsh", "powershell"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ran = true
		t.Run(name, func(t *testing.T) {
			fn(t, &ShellTool{shell: proc.Shell{Kind: proc.ShellPowerShell, Path: path}})
		})
	}
	if !ran {
		t.Skip("no PowerShell on this machine")
	}
}

func TestPowerShellRunsACommand(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, sh *ShellTool) {
		result := sh.ExecuteApproved(context.Background(), map[string]any{
			"command": `Write-Output 'a"b\c'; Write-Output "中文 héllo"`,
		}, t.TempDir())
		if !result.Success {
			t.Fatalf("failed: %s %s", result.Error, result.Output)
		}
		for _, want := range []string{`a"b\c`, "中文 héllo"} {
			if !strings.Contains(result.Output, want) {
				t.Errorf("output %q lacks %q", result.Output, want)
			}
		}
	})
}

func TestPowerShellRunsAScriptPastTheCommandLineCap(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, sh *ShellTool) {
		result := sh.ExecuteApproved(context.Background(), map[string]any{
			"command": "# " + strings.Repeat("x", 20000) + "\nWrite-Output 'long 中文'",
		}, t.TempDir())
		if !result.Success || !strings.Contains(result.Output, "long 中文") {
			t.Fatalf("Success=%v Error=%q Output=%q", result.Success, result.Error, result.Output)
		}
	})
}

func TestPowerShellReportsTheExitCodeOfANativeProgram(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, sh *ShellTool) {
		// PowerShell itself, run as a native program that exits 3.
		result := sh.ExecuteApproved(context.Background(), map[string]any{
			"command": `& (Get-Process -Id $PID).Path -NoProfile -Command 'exit 3'`,
		}, t.TempDir())
		if result.Success || result.Error != "exit code 3" {
			t.Errorf("Success=%v Error=%q, want exit code 3", result.Success, result.Error)
		}
	})
}

func TestPowerShellTracksAChangedDirectory(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, sh *ShellTool) {
		cwd := t.TempDir()
		sub := filepath.Join(cwd, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		result := sh.ExecuteApproved(context.Background(), map[string]any{
			"command": "Set-Location sub",
		}, cwd)
		if !result.Success {
			t.Fatalf("failed: %s %s", result.Error, result.Output)
		}
		resp, _ := result.HookResponse.(map[string]any)
		got, _ := resp["cwd"].(string)
		want, _ := filepath.EvalSymlinks(sub)
		if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
			t.Errorf("tracked cwd = %q, want %q", got, sub)
		}
	})
}
