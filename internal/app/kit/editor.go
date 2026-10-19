package kit

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// StartExternalEditor launches the user's preferred editor for the given file.
// The msgFn callback converts the editor's exit error into a tea.Msg for the
// caller's message loop.
func StartExternalEditor(filePath string, msgFn func(error) tea.Msg) tea.Cmd {
	cmd := EditorCommand(filePath)
	// The program's output is a wrapper, and an editor needs the terminal itself.
	cmd.Stdout = os.Stdout
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return msgFn(err)
	})
}

// EditorCommand opens filePath in the user's editor: $EDITOR, then $VISUAL,
// skipping one that is not installed, then the platform's usual editors. The
// value may be a path with spaces (C:\Program Files\...) or carry arguments
// ("code -w").
func EditorCommand(filePath string) *exec.Cmd {
	for _, env := range []string{"EDITOR", "VISUAL"} {
		value := strings.TrimSpace(os.Getenv(env))
		if value == "" {
			continue
		}
		if _, err := exec.LookPath(value); err == nil {
			return exec.Command(value, filePath)
		}
		if args := strings.Fields(value); len(args) > 1 {
			if _, err := exec.LookPath(args[0]); err == nil {
				return exec.Command(args[0], append(args[1:], filePath)...)
			}
		}
	}
	fallbacks := []string{"vim", "nano", "vi"}
	if runtime.GOOS == "windows" {
		fallbacks = []string{"notepad"}
	}
	for _, e := range fallbacks {
		if _, err := exec.LookPath(e); err == nil {
			return exec.Command(e, filePath)
		}
	}
	return exec.Command(fallbacks[len(fallbacks)-1], filePath)
}
