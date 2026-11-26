package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
)

// memoryOp is a Read, Write or Edit of an auto-memory file, or a plain `rm`
// of some.
type memoryOp struct {
	verb    string   // Saved, Updated, Read, Deleted
	index   bool     // the op is on a MEMORY.md
	path    string   // the (first) file
	removed []string // every file a Deleted op removes
}

func parseMemoryOp(store system.MemoryStore, toolName, input string) (memoryOp, bool) {
	if toolName == tool.ToolBash {
		return parseMemoryRemove(store, input)
	}
	if toolName != tool.ToolWrite && toolName != tool.ToolEdit && toolName != tool.ToolRead {
		return memoryOp{}, false
	}
	var args struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(input), &args) != nil || !store.Contains(args.FilePath) {
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
func parseMemoryRemove(store system.MemoryStore, input string) (memoryOp, bool) {
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(input), &args)
	files, ok := setting.RemovedFiles(args.Command)
	if !ok || slices.ContainsFunc(files, func(f string) bool { return !store.Contains(f) }) {
		return memoryOp{}, false
	}
	return memoryOp{verb: "Deleted", path: files[0], removed: files}, true
}

// memoryStep reports whether an assistant step only kept memory: no text,
// and every call is hidden bookkeeping that did not fail. Explicit reads and
// attached recalls stay in their own tool step instead of a turn-end footer.
func (p RenderContext) memoryStep(msg core.ChatMessage, idx int) bool {
	if msg.Role != core.ChatAssistant || strings.TrimSpace(msg.Content) != "" || len(msg.ToolCalls) == 0 || msg.ToolCallsExpanded {
		return false
	}
	results := p.InlinedResults.resultsFor(idx)
	for _, tc := range msg.ToolCalls {
		op, ok := parseMemoryOp(p.AutoMemoryStore, tc.Name, tc.Input)
		if !ok || results[tc.ID].IsError || (op.verb == "Read" && !op.index) {
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

// renderMemoryRead replaces the compact file operation with a contextual
// memory notice. Only a completed, successful read uses the past-tense label.
func renderMemoryRead(tc core.ToolCall, op memoryOp, params ToolCallsParams) string {
	marker := toolCallStyle.Render(widgets.MemoryMark)
	verb := "read"
	if _, done := params.ResultMap[tc.ID]; !done {
		verb = "reading"
		if params.SpinnerView != "" && toolCallIcon(tc, params) == params.SpinnerView {
			marker = widgets.LiveMemory(params.Frame)
		}
	}
	return renderMemoryNotice(marker, verb, memoryTitle(op.path), params.Width)
}

func renderMemoryNotice(marker, verb, title string, width int) string {
	title = strings.Join(strings.Fields(xansi.Strip(title)), " ")
	return renderGutteredLine(marker+" ", "  ", sideMarkStyle, "Memory · "+verb+": "+title, width)
}

func isMemoryUpdateNotice(content string) bool {
	text, ok := strings.CutPrefix(content, "Memory · ")
	return ok && (strings.HasPrefix(text, "saved ") || strings.HasPrefix(text, "updated ") || strings.HasPrefix(text, "forgot "))
}

func renderMemoryUpdateNotice(content string, width int) string {
	return renderGutteredLine(widgets.MemoryMark+" ", "  ", sideMarkStyle, content, width)
}

var recalledMemoryPath = regexp.MustCompile("(?m)^Memory `([^`\\r\\n]+)` \\((?:user|project) store(?:, [^)\\r\\n]*)?\\):\\r?$")

// recalledMemoryPaths reads only the harness's recall blocks, never matching
// a similar heading in ordinary tool output. Paths are unique within a result.
func recalledMemoryPaths(content string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, block := range reminder.Blocks(content) {
		if block.Source != reminder.ProviderMemoryRecall {
			continue
		}
		for _, match := range recalledMemoryPath.FindAllStringSubmatch(block.Text, -1) {
			path := match[1]
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

const memoryRecallNoticePrefix = "/memory-recalled "

func compactRecallNotice(after string, ids []string) *core.AutoMemoryRecallNotice {
	titles := make([]string, 0, len(ids))
	for _, id := range ids {
		name := strings.TrimSuffix(filepath.Base(id), ".md")
		titles = append(titles, strings.Join(strings.Fields(xansi.Strip(name)), " "))
	}
	return &core.AutoMemoryRecallNotice{AfterMessageID: after, Titles: titles, Compact: true}
}

// EncodeRecallNotice keeps the existing annotation format for replay.
func EncodeRecallNotice(notice *core.AutoMemoryRecallNotice) string {
	data, _ := json.Marshal(notice)
	return memoryRecallNoticePrefix + string(data)
}

// recalledMemoryNotice captures the titles when the filtered native result
// reaches the model. ToolEnd precedes that filter, so the raw tool result the
// UI already drew cannot carry this notice. Persisting a display-only notice
// here keeps it before the next inference and in the same place after resume.
func recalledMemoryNotice(store system.MemoryStore, content, afterMessageID string) *core.AutoMemoryRecallNotice {
	var titles []string
	for _, path := range recalledMemoryPaths(content) {
		title := strings.TrimSuffix(filepath.Base(path), ".md")
		// Never read a file outside the configured stores for a display title.
		if store.Contains(path) {
			title = memoryTitle(path)
		}
		titles = append(titles, title)
	}
	if len(titles) == 0 {
		return nil
	}
	return &core.AutoMemoryRecallNotice{AfterMessageID: afterMessageID, Titles: titles}
}

func renderRecordedRecallNotice(content string, width int) string {
	var data core.AutoMemoryRecallNotice
	if json.Unmarshal([]byte(content), &data) != nil {
		return ""
	}
	return renderMemoryRecallNotice(&data, width)
}

func renderMemoryRecallNotice(data *core.AutoMemoryRecallNotice, width int) string {
	if data.Compact && len(data.Titles) > 0 {
		if width <= 0 {
			width = 80
		}
		label := "recalled 1 note"
		if n := len(data.Titles); n > 1 {
			label = fmt.Sprintf("recalled %d notes", n)
		}
		line := widgets.MemoryMark + " Memory · " + label
		titles := strings.Join(data.Titles, ", ")
		if titles != "" && xansi.StringWidth(line+" — "+titles) <= width {
			line += " — " + titles
		}
		return sideMarkStyle.Render(xansi.Truncate(line, width, "…")) + "\n"
	}
	var sb strings.Builder
	for _, title := range data.Titles {
		sb.WriteString(renderMemoryNotice(toolCallStyle.Render(widgets.MemoryMark), "recalled", title, width))
	}
	return sb.String()
}

// MemoryEntry is one memory a turn touched.
type MemoryEntry struct {
	Verb  string // "recalled", "used", "saved", "updated" or "forgot"
	Path  string
	Title string // its index title, else its description, else its slug
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
	for _, msg := range msgs[start:] {
		for _, tc := range msg.ToolCalls {
			op, ok := parseMemoryOp(m.AutoMemoryStore, tc.Name, tc.Input)
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
		kept = append(kept, e)
	}
	return kept
}

// withoutRecall drops memory-recall reminders from a tool result read back
// from a transcript: they are for the model; the contextual recall notice
// names them without displaying their private contents.
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
