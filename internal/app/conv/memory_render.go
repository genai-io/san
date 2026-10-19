package conv

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// memoryOp is a Read, Write or Edit of an auto-memory file, shown as what it
// means ("Saved memory · prefers pnpm") rather than as a file operation.
type memoryOp struct {
	verb    string // Saved, Updated, Recalled
	subject string // the memory's description or name; "" for the index
	file    string
}

func parseMemoryOp(toolName, input string) (memoryOp, bool) {
	if toolName != tool.ToolWrite && toolName != tool.ToolEdit && toolName != tool.ToolRead {
		return memoryOp{}, false
	}
	var args struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if json.Unmarshal([]byte(input), &args) != nil || !setting.IsAutoMemoryPath(args.FilePath) {
		return memoryOp{}, false
	}
	op := memoryOp{file: filepath.Base(args.FilePath)}
	switch toolName {
	case tool.ToolRead:
		op.verb = "Recalled"
	case tool.ToolEdit:
		op.verb = "Updated"
	default:
		op.verb = "Saved"
	}
	if op.file == system.AutoMemoryIndexName {
		if op.verb == "Saved" {
			op.verb = "Updated"
		}
		return op, true
	}
	op.subject = frontmatterField(args.Content, "description")
	if op.subject == "" {
		op.subject = strings.TrimSuffix(op.file, ".md")
	}
	return op, true
}

// frontmatterField returns key's value from content's leading frontmatter.
func frontmatterField(content, key string) string {
	fm, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return ""
	}
	fm, _, _ = strings.Cut(fm, "\n---")
	for line := range strings.SplitSeq(fm, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// renderMemoryCall draws the call row: "● Saved memory · prefers pnpm".
func renderMemoryCall(op memoryOp, width int, icon string) string {
	head := op.verb + " memory"
	if op.subject == "" {
		head += " index"
	}
	row := toolCallStyle.Width(2).Render(icon) + memoryCallStyle.Render(head)
	if op.subject != "" {
		tail := " · " + op.subject
		if width > 0 {
			tail = kit.TruncateText(tail, max(0, width-lipgloss.Width(row)))
		}
		row += toolResultStyle.Render(tail)
	}
	return row + "\n"
}

// renderMemoryResult folds a memory call's result to one trailer naming the
// file; the diff or file body stays behind ctrl+o.
func renderMemoryResult(data ToolResultData) (string, bool) {
	if !data.Nested || data.IsError || data.Expanded {
		return "", false
	}
	op, ok := parseMemoryOp(data.ToolName, data.ToolInput)
	if !ok {
		return "", false
	}
	trailer := op.file
	if details, ok := data.Details.(toolresult.FileChangeDetails); ok {
		trailer += " · " + fileChangeSummary(details)
	}
	return renderNestedToolTrailer(trailer, toolResultStyle), true
}

var memoryCallStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
