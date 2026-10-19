package conv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

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

// MemoryEntry is one memory a turn touched.
type MemoryEntry struct {
	Verb  string // "used", "saved" or "updated"
	Path  string
	Title string // its index title, else its description, else its slug
}

var indexLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+\.md)\)`)

// TurnMemory lists what the latest turn did with memory, back to its user
// message, in order: memories read (used) and written (saved or updated, once
// per file). Index reads and writes and failed calls don't count.
func (m *ConversationModel) TurnMemory() []MemoryEntry {
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
	var out []MemoryEntry
	seen := map[string]bool{} // verb + path
	add := func(e MemoryEntry) {
		key := e.Verb + e.Path
		if e.Verb == "updated" && seen["saved"+e.Path] || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, e)
	}
	for _, m := range msgs[start:] {
		for _, tc := range m.ToolCalls {
			op, ok := parseMemoryOp(tc.Name, tc.Input)
			if !ok || failed[tc.ID] || op.subject == "" {
				continue
			}
			e := MemoryEntry{Verb: strings.ToLower(op.verb), Path: op.path}
			if e.Verb == "read" {
				e.Verb = "used"
			}
			e.Title = memoryTitle(op.path)
			add(e)
		}
	}
	return out
}

// memoryTitle names a memory as its store's index does ("[Go dev](go.md)"),
// falling back to its description, then its slug.
func memoryTitle(path string) string {
	index, _ := os.ReadFile(filepath.Join(filepath.Dir(path), system.AutoMemoryIndexName))
	for _, l := range indexLink.FindAllStringSubmatch(string(index), -1) {
		if l[2] == filepath.Base(path) {
			return l[1]
		}
	}
	body, _ := os.ReadFile(path)
	if d := frontmatterField(string(body), "description"); d != "" {
		return d
	}
	return strings.TrimSuffix(filepath.Base(path), ".md")
}
