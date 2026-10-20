package conv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

// MemoryStores are the session's auto-memory stores, set by the app whenever
// it resolves them; a call into one renders as memory, not as a file op.
var MemoryStores system.MemoryStores

// memoryOp is a Read, Write or Edit of an auto-memory file, or a plain `rm`
// of some.
type memoryOp struct {
	verb    string   // Saved, Updated, Read, Deleted
	index   bool     // the op is on a MEMORY.md
	path    string   // the (first) file
	removed []string // every file a Deleted op removes
}

func parseMemoryOp(toolName, input string) (memoryOp, bool) {
	if toolName == tool.ToolBash {
		return parseMemoryRemove(input)
	}
	if toolName != tool.ToolWrite && toolName != tool.ToolEdit && toolName != tool.ToolRead {
		return memoryOp{}, false
	}
	var args struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(input), &args) != nil || !MemoryStores.Contains(args.FilePath) {
		return memoryOp{}, false
	}
	op := memoryOp{path: args.FilePath, index: filepath.Base(args.FilePath) == system.AutoMemoryIndexName}
	switch toolName {
	case tool.ToolRead:
		op.verb = "Read"
	case tool.ToolEdit:
		op.verb = "Updated"
	default:
		op.verb = "Saved"
	}
	return op, true
}

// parseMemoryRemove reads a plain `rm` of memory files as one Deleted op.
func parseMemoryRemove(input string) (memoryOp, bool) {
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(input), &args)
	files, ok := setting.RemovedFiles(args.Command)
	if !ok || slices.ContainsFunc(files, func(f string) bool { return !MemoryStores.Contains(f) }) {
		return memoryOp{}, false
	}
	return memoryOp{verb: "Deleted", path: files[0], removed: files}, true
}

// memoryStep reports whether an assistant step only kept memory: no text, and
// every call a memory call that didn't fail. Its calls don't draw; the turn's
// memory line reports them.
func (p RenderContext) memoryStep(msg core.ChatMessage, idx int) bool {
	if msg.Role != core.ChatAssistant || strings.TrimSpace(msg.Content) != "" || len(msg.ToolCalls) == 0 || msg.ToolCallsExpanded {
		return false
	}
	results := p.InlinedResults.resultsFor(idx)
	for _, tc := range msg.ToolCalls {
		if _, ok := parseMemoryOp(tc.Name, tc.Input); !ok || results[tc.ID].IsError {
			return false
		}
	}
	return true
}

// memoryStepView draws a memory step without its calls. Its thinking stays, in
// scrollback too: it was on screen before the calls arrived, and taking it back
// would flash. While the step is the live tail, its spinner keeps the turn
// looking alive.
func (p RenderContext) memoryStepView(msg core.ChatMessage, idx int) string {
	tail := p.Interactive && p.StreamActive
	for _, later := range p.Messages[idx+1:] {
		tail = tail && later.Role == core.ChatUser && later.ToolResult != nil
	}
	msg.ToolCalls = nil
	p.StreamActive = tail // settled, it has no spinner or filler to draw
	body := strings.TrimRight(renderAssistantWithTools(p, msg, idx, tail), "\n")
	if strings.TrimSpace(xansi.Strip(body)) == "" {
		return ""
	}
	return "\n" + body + "\n"
}

// MemoryEntry is one memory a turn touched.
type MemoryEntry struct {
	Verb  string // "recalled", "used", "saved", "updated" or "forgot"
	Path  string
	Title string // its index title, else its description, else its slug
	Store string // "user" or "project"
}

var turnVerb = map[string]string{"Read": "used", "Saved": "saved", "Updated": "updated"}

var indexLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+\.md)\)`)

// TurnMemory lists what the latest turn did with memory, back to its user
// message, one entry per memory in the order first touched: recalled (attached
// to a tool result), used (read), saved or updated (a write outranks a read),
// or forgot — touched this turn and gone by its end. The turn starts at the
// message openedBy names, else at the last user message; recalled are the
// memories attached to it, which the agent's tool results carry but the
// screen's do not. Index files and failed calls don't count.
func (m *ConversationModel) TurnMemory(openedBy string, recalled []string) []MemoryEntry {
	msgs := m.Messages
	start := len(msgs)
	for start > 0 && (msgs[start-1].Role != core.ChatUser || msgs[start-1].ToolResult != nil) {
		start--
	}
	if i := slices.IndexFunc(msgs, func(c core.ChatMessage) bool { return openedBy != "" && c.ID == openedBy }); i >= 0 {
		start = i + 1
	}
	failed := map[string]bool{}
	for _, m := range msgs[start:] {
		if m.ToolResult != nil && m.ToolResult.IsError {
			failed[m.ToolResult.ToolCallID] = true
		}
	}
	var out []MemoryEntry
	at := map[string]int{}         // path → its entry
	dropped := map[string]string{} // path → title, from index lines edited away
	note := func(path, verb string) {
		i, ok := at[path]
		if !ok {
			i = len(out)
			at[path] = i
			out = append(out, MemoryEntry{Path: path, Verb: verb})
		}
		// A write outranks a read, and a read outranks being shown.
		if e := &out[i]; e.Verb == "recalled" || (e.Verb == "used" && verb != "recalled") {
			e.Verb = verb
		}
	}
	for _, path := range recalled {
		note(filepath.Clean(path), "recalled")
	}
	for _, m := range msgs[start:] {
		for _, tc := range m.ToolCalls {
			op, ok := parseMemoryOp(tc.Name, tc.Input)
			if !ok || failed[tc.ID] {
				continue
			}
			if op.verb == "Deleted" {
				for _, f := range op.removed {
					note(filepath.Clean(f), "forgot")
				}
				continue
			}
			if !op.index {
				note(filepath.Clean(op.path), turnVerb[op.verb])
				continue
			}
			var edit struct {
				Old string `json:"old_string"`
			}
			_ = json.Unmarshal([]byte(tc.Input), &edit)
			for _, l := range indexLink.FindAllStringSubmatch(edit.Old, -1) {
				dropped[filepath.Join(filepath.Dir(op.path), l[2])] = l[1] // a forgotten memory keeps its title
			}
		}
	}
	kept := out[:0]
	for _, e := range out {
		e.Title = memoryTitle(e.Path)
		if _, err := os.Stat(e.Path); err != nil {
			e.Verb = "forgot"
			if t, ok := dropped[e.Path]; ok {
				e.Title = t
			}
		}
		e.Store = "project"
		if filepath.Dir(e.Path) == MemoryStores.User {
			e.Store = "user"
		}
		kept = append(kept, e)
	}
	return kept
}

// withoutRecall drops memory-recall reminders from a tool result read back
// from a transcript: they are for the model, and the memory line reports them.
func withoutRecall(content string) string {
	for _, b := range reminder.Blocks(content) {
		if b.Source == reminder.ProviderMemoryRecall {
			content = strings.Replace(content, b.Text, "", 1)
		}
	}
	return strings.TrimRight(content, "\n")
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
	if d := system.FrontmatterField(string(body), "description"); d != "" {
		return d
	}
	return strings.TrimSuffix(filepath.Base(path), ".md")
}
