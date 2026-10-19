package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/fs"
)

// autoMemory is the auto-memory stores the main agent maintains, zero when
// auto memory is off (setting, or SAN_DISABLE_AUTO_MEMORY).
func (m *model) autoMemory() system.MemoryStores {
	if !m.services.Setting.AutoMemory() {
		return system.MemoryStores{}
	}
	return setting.MemoryStoresFor(m.env.CWD)
}

// autoMemoryStore returns the store a Write/Edit call targets with a markdown
// file, or "": the agent maintains its stores without prompting. Symlinks are
// resolved so a link cannot carry the write outside the store.
func autoMemoryStore(s system.MemoryStores, toolName string, args map[string]any) string {
	if toolName != "Write" && toolName != "Edit" {
		return ""
	}
	path, _ := args["file_path"].(string)
	if !strings.HasSuffix(path, ".md") || !filepath.IsAbs(path) {
		return ""
	}
	for _, dir := range []string{s.User, s.Project} {
		if dir != "" && setting.WithinDirectory(path, dir) {
			return dir
		}
	}
	return ""
}

// permitAutoMemoryWrite reports whether a call is the agent keeping its own
// memory, which needs no approval in any mode — headless and don't-ask
// included, where a prompt would become a refusal. Only a deny rule stops it.
func permitAutoMemoryWrite(s system.MemoryStores, toolName string, args map[string]any, denyRule func(string, map[string]any) string) bool {
	return autoMemoryStore(s, toolName, args) != "" && denyRule(toolName, args) == ""
}

// afterMemoryWrite runs after a successful Write/Edit into the auto-memory
// store: it stamps the file's `modified` time and, when the index changed,
// tells the model right away what is wrong with it instead of next session.
func afterMemoryWrite(s system.MemoryStores, c sdkagent.PostToolContext) *sdkagent.Result {
	if c.Err != nil {
		return nil
	}
	var args map[string]any
	if json.Unmarshal([]byte(c.Call.Input), &args) != nil {
		return nil
	}
	dir := autoMemoryStore(s, c.Call.Name, args)
	if dir == "" {
		return nil
	}
	path, _ := args["file_path"].(string)
	var notes []string
	if stamped, err := system.StampModified(path, time.Now()); err == nil && stamped {
		// The stamp is ours, not an outside change the next Edit should balk at.
		fs.RecordFileWritten(path)
		notes = append(notes, "Recorded the write time as `modified` in its frontmatter and tidied its keys; the file needs no follow-up edit.")
	}
	if dup := s.Duplicates(); len(dup) > 0 {
		notes = append(notes, "Your memory has duplicates:\n- "+strings.Join(dup, "\n- "))
	}
	if filepath.Base(path) == system.AutoMemoryIndexName {
		if _, problems := system.LoadAutoMemoryAt(dir); len(problems) > 0 {
			notes = append(notes, "Your memory index needs fixing:\n- "+strings.Join(problems, "\n- "))
		}
	}
	if len(notes) == 0 {
		return nil
	}
	out := c.Result
	out.Content = ai.TextContent(strings.TrimSpace(c.Result.Content.Text() + "\n\n" + strings.Join(notes, "\n\n")))
	return &out
}

// memoryLine is the turn's memory summary, grouped by verb in the order
// things happened — one line for a single verb, an aligned block for several:
//
//	◇ memory  used     Go dev, Release freeze
//	          updated  Release freeze · 10/30 → 11/5 · project
//
// Writes name their store; each line is cut to width. "" when the turn left
// memory alone.
func memoryLine(s system.MemoryStores, entries []conv.MemoryEntry, width int) string {
	var verbs []string
	items := map[string][]string{}
	for _, e := range entries {
		if _, ok := items[e.Verb]; !ok {
			verbs = append(verbs, e.Verb)
		}
		item := e.Title
		if e.Change != "" {
			item += " · " + e.Change
		}
		if e.Verb != "used" {
			store := "project"
			if filepath.Dir(e.Path) == s.User {
				store = "user"
			}
			item += " · " + store
		}
		items[e.Verb] = append(items[e.Verb], item)
	}
	if len(verbs) == 0 {
		return ""
	}
	const head = "◇ memory  " // the notice indents it under the answer
	verbWidth := 0
	if len(verbs) > 1 {
		verbWidth = len("updated")
	}
	var lines []string
	for i, v := range verbs {
		lead := memoryLabelStyle.Render(head)
		if i > 0 {
			lead = strings.Repeat(" ", len([]rune(head)))
		}
		verb := memoryLabelStyle.Render(v + strings.Repeat(" ", max(verbWidth-len(v), 0)) + "  ")
		text := strings.Join(items[v], ", ")
		if width > 0 {
			text = kit.TruncateText(text, max(8, width-lipgloss.Width(lead)-lipgloss.Width(verb)))
		}
		lines = append(lines, lead+verb+memoryItemStyle.Render(text))
	}
	return strings.Join(lines, "\n")
}

var (
	memoryLabelStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	memoryItemStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
)
