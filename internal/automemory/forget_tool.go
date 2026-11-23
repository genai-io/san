package automemory

import (
	"slices"
	"strings"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// forgetTools offers only shell schemas already present in the main prefix.
// Bash is also the schema name when the configured interpreter is zsh.
func forgetTools(req *ai.Request) []string {
	if req.ToolChoice != ai.ToolChoiceAuto {
		return nil
	}
	var names []string
	for _, schema := range req.Tools {
		if tool.IsShellTool(schema.Name) {
			names = append(names, schema.Name)
		}
	}
	return names
}

// shellForget interprets one literal delete as a proposal. It never executes a
// shell or accepts a path; apply resolves the note against the pinned store.
func shellForget(call ai.ToolCall, allowed []string) (proposal, error) {
	var args struct {
		Command       string `json:"command"`
		Description   string `json:"description"`
		ExecutionMode string `json:"execution_mode"`
		Timeout       int    `json:"timeout_ms"`
	}
	if !slices.Contains(allowed, call.Name) || len(call.Input) > 4096 ||
		core.DecodeStrictJSON(strings.NewReader(call.Input), &args) != nil ||
		(args.ExecutionMode != "" && args.ExecutionMode != "foreground") {
		return proposal{}, errInvalidProposal
	}
	var file string
	switch call.Name {
	case tool.ToolBash:
		files, ok := setting.RemovedFiles(args.Command)
		if !ok || len(files) != 1 {
			return proposal{}, errInvalidProposal
		}
		file = files[0]
	case tool.ToolPowerShell:
		command, ok := strings.CutPrefix(strings.TrimSpace(args.Command), "Remove-Item -LiteralPath ")
		if !ok {
			return proposal{}, errInvalidProposal
		}
		file = strings.TrimSpace(command)
		if len(file) >= 2 && (file[0] == '\'' || file[0] == '"') && file[0] == file[len(file)-1] {
			file = file[1 : len(file)-1]
		}
	}
	if !validName(file) {
		return proposal{}, errInvalidProposal
	}
	return proposal{Action: actionForget, File: file}, nil
}
