package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/infra/proc"
)

// Exercise dispatch rather than just the diagnostic helper: validation errors
// precede shell startup, and each host must emit at most one warning.
func TestShellWarningDispatch(t *testing.T) {
	if args := os.Getenv("SAN_TEST_STARTUP_ARGS"); args != "" {
		rootCmd.SetArgs(strings.Fields(args))
		main()
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell preference")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed")
	}
	for _, tc := range []struct {
		args, message string
		warnings      int
	}{
		{"agent run", "--prompt is required", 0},
		{"--print=", "--print requires a prompt", 0},
		{"agent run --prompt test", "", 1},
		{"--print=test", "", 1},
		{"acp", "", 1},
	} {
		t.Run(tc.args, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestShellWarningDispatch$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "SAN_SHELL=", "SHELL=/not-installed/fish", "SAN_TEST_STARTUP_ARGS="+tc.args)
			cmd.Stdin = strings.NewReader("")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, err := cmd.Output()
			got := stderr.String()
			const warning = "falling back to bash"
			first, _, _ := strings.Cut(got, "\n")
			if err == nil || len(stdout) != 0 || !strings.Contains(got, tc.message) || strings.Count(got, warning) != tc.warnings || (tc.warnings > 0 && !strings.Contains(first, warning)) {
				t.Fatalf("stdout=%q stderr=%q err=%v", stdout, got, err)
			}
		})
	}
}

func TestShellFallbackWarning(t *testing.T) {
	if os.Getenv("SAN_TEST_SHELL_WARNING") == "1" {
		warnShellFallback()
		fmt.Print(proc.DefaultShellName())
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores the Unix SHELL preference")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	type shellCase struct {
		preference, want string
		warning          bool
	}
	cases := []shellCase{{bash, "bash", false}, {"/not-installed/fish", "bash", true}}
	if zsh, err := exec.LookPath("zsh"); err == nil {
		cases = append(cases, shellCase{zsh, "zsh", false})
	}
	for _, tc := range cases {
		t.Run(tc.preference, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestShellFallbackWarning$")
			cmd.Env = append(os.Environ(), "SHELL="+tc.preference, "SAN_SHELL=", "SAN_TEST_SHELL_WARNING=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil || !strings.HasPrefix(string(out), tc.want) {
				t.Fatalf("selected shell = %q, %v (%s)", out, err, stderr.String())
			}
			if got := stderr.String(); strings.Count(got, "falling back to bash") != map[bool]int{true: 1}[tc.warning] {
				t.Fatalf("fallback diagnostic = %q", got)
			}
		})
	}
}
