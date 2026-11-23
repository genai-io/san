package automemory

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Authorize one literal removal of an observed note, never a general shell
// command. The registered shell executes the equivalent command on its copy.
func forgetProposalFor(call ai.ToolCall, dir string, notes []noteRevision, shells []string) (proposal, error) {
	var args struct {
		Command     string `json:"command"`
		Description string `json:"description"`
		Mode        string `json:"execution_mode"`
		Timeout     int    `json:"timeout_ms"`
	}
	if !slices.Contains(shells, call.Name) || !decodeUpdateInput(call.Input, &args) ||
		(args.Mode != "" && args.Mode != "foreground") {
		return proposal{}, errInvalidProposal
	}
	var target string
	if call.Name == tool.ToolBash {
		files, ok := setting.RemovedFiles(args.Command)
		if !ok || len(files) != 1 {
			return proposal{}, errInvalidProposal
		}
		target = files[0]
	}
	for _, note := range notes {
		absolute := filepath.Join(dir, note.name)
		if call.Name == tool.ToolBash && (target == note.name || target == absolute) ||
			call.Name == tool.ToolPowerShell && powerShellRemoval(args.Command, note.name, absolute) {
			return proposal{Action: actionForget, File: note.name, Shell: call.Name}, nil
		}
	}
	return proposal{}, errInvalidProposal
}

func powerShellRemoval(command, filename, absolute string) bool {
	const prefix = "Remove-Item -LiteralPath "
	command = strings.TrimSpace(command)
	if len(command) <= len(prefix) || !strings.EqualFold(command[:len(prefix)], prefix) {
		return false
	}
	target := strings.TrimSpace(command[len(prefix):])
	return target == filename || target == "'"+filename+"'" || target == `"`+filename+`"` ||
		target == "'"+strings.ReplaceAll(absolute, "'", "''")+"'"
}

func forgetCommand(shell, file string) string {
	if shell == tool.ToolPowerShell {
		return "Microsoft.PowerShell.Management\\Remove-Item -LiteralPath '" + file + "' -ErrorAction Stop"
	}
	return "command rm -- '" + file + "'"
}
