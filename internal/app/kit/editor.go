package kit

import (
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// StartExternalEditor launches the user's preferred editor for the given file.
// The msgFn callback converts the editor's exit error into a tea.Msg for the
// caller's message loop.
func StartExternalEditor(filePath string, msgFn func(error) tea.Msg) tea.Cmd {
	editor := GetEditor()
	cmd := exec.Command(editor, filePath)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return msgFn(err)
	})
}

// GetEditor returns the user's preferred text editor.
func GetEditor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	for _, e := range []string{"vim", "nano", "vi"} {
		if _, err := exec.LookPath(e); err == nil {
			return e
		}
	}
	return "vi"
}
