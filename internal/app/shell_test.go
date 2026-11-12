package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/proc"
)

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
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, "SHELL=") && !strings.HasPrefix(env, "SAN_SHELL=") && !strings.HasPrefix(env, "SAN_TEST_SHELL_WARNING=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "SHELL="+tc.preference, "SAN_SHELL=", "SAN_TEST_SHELL_WARNING=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil || !strings.HasPrefix(string(out), tc.want) {
				t.Fatalf("selected shell = %q, %v (%s)", out, err, stderr.String())
			}
			if got := stderr.String(); tc.warning != strings.Contains(got, "falling back to bash") {
				t.Fatalf("fallback diagnostic = %q", got)
			}
		})
	}
}
