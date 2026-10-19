package conv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

// memoryOp is a Read, Write or Edit of an auto-memory file, shown as what it
// means ("Saved memory · prefers pnpm") rather than as a file operation.
type memoryOp struct {
	verb    string // Saved, Updated, Read
	subject string // the memory's description or name; "" for the index
	file    string
	path    string
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
	op := memoryOp{file: filepath.Base(args.FilePath), path: args.FilePath}
	switch toolName {
	case tool.ToolRead:
		op.verb = "Read"
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

// memoryFold keeps a message's memory calls out of the main flow. It returns
// the calls to hide and, once they finish, one summary in their place: "Read 2
// memories · a, b" when memories were read, "" for index reads and writes
// (the turn-end card reports writes). While they run, Thinking stands in.
// Expanded calls and failed ones are left to render as themselves.
func memoryFold(params ToolCallsParams) (hidden map[string]bool, summary string) {
	if params.ToolCallsExpanded {
		return nil, ""
	}
	var names []string
	running := false
	for _, tc := range params.ToolCalls {
		op, ok := parseMemoryOp(tc.Name, tc.Input)
		if !ok {
			continue
		}
		result, done := params.ResultMap[tc.ID]
		if done && result.IsError {
			continue
		}
		if hidden == nil {
			hidden = map[string]bool{}
		}
		hidden[tc.ID] = true
		running = running || !done
		if op.verb == "Read" && op.subject != "" { // an index read is routine
			names = append(names, strings.TrimSuffix(op.file, ".md"))
		}
	}
	switch {
	case running || len(names) == 0:
		return hidden, ""
	case len(names) == 1:
		return hidden, "Read 1 memory · " + names[0]
	}
	return hidden, fmt.Sprintf("Read %d memories · %s", len(names), strings.Join(names, ", "))
}

// memoryRow draws the summary on its own thinking-style line.
func memoryRow(summary string, width int) string {
	if summary == "" {
		return ""
	}
	gutter := thinkingGutter(true)
	if width > 0 {
		summary = kit.TruncateText(summary, max(1, width-lipgloss.Width(gutter)))
	}
	return gutter + ThinkingStyle.Render(summary) + "\n"
}

// joinThinkingLine appends the summary to the last written line of a rendered
// thinking tail (past glamour's padding and trailing blank lines), so
// "✦ Checking the release · Read 1 memory · freeze" reads as one thought. It
// reports false when there is no tail or too little room.
func joinThinkingLine(base, summary string, width int) (string, bool) {
	if summary == "" {
		return base, false
	}
	lines := strings.Split(base, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		text := strings.TrimRight(xansi.Strip(lines[i]), " ")
		if strings.TrimSpace(text) == "" {
			continue
		}
		used := lipgloss.Width(text)
		tail := " · " + summary
		if width > 0 {
			if width-used < 24 {
				return base, false
			}
			tail = kit.TruncateText(tail, width-used)
		}
		lines[i] = xansi.Truncate(lines[i], used, "") + "\x1b[m" + ThinkingStyle.Render(tail)
		return strings.Join(lines, "\n"), true
	}
	return base, false
}

// MemoryChange is a memory file a turn saved or updated.
type MemoryChange struct {
	Verb        string // "saved" or "updated"
	Path        string
	Description string
}

// TurnMemoryChanges lists the memories the latest turn wrote, back to its user
// message: a file created then edited counts once as saved, and index edits are
// left out since they follow from the topic writes. Failed calls don't count.
func (m *ConversationModel) TurnMemoryChanges() []MemoryChange {
	msgs := m.Messages
	start := len(msgs)
	for start > 0 && (msgs[start-1].Role != core.ChatUser || msgs[start-1].ToolResult != nil) {
		start--
	}
	failed := map[string]bool{}
	for _, m := range msgs[start:] {
		if m.ToolResult != nil && m.ToolResult.IsError {
			failed[m.ToolResult.ToolCallID] = true
		}
	}
	var out []MemoryChange
	seen := map[string]bool{}
	for _, m := range msgs[start:] {
		for _, tc := range m.ToolCalls {
			op, ok := parseMemoryOp(tc.Name, tc.Input)
			if !ok || op.verb == "Read" || op.subject == "" || failed[tc.ID] || seen[op.path] {
				continue
			}
			seen[op.path] = true
			body, _ := os.ReadFile(op.path)
			out = append(out, MemoryChange{
				Verb:        strings.ToLower(op.verb),
				Path:        op.path,
				Description: frontmatterField(string(body), "description"),
			})
		}
	}
	return out
}
