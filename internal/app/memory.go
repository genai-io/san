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
	"github.com/genai-io/san/internal/tool"
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
// memory — a Write/Edit, or a plain `rm` of memory files — which needs no
// approval in any mode, headless and don't-ask included, where a prompt would
// become a refusal. Only a deny rule stops it.
func permitAutoMemoryWrite(s system.MemoryStores, toolName string, args map[string]any, denyRule func(string, map[string]any) string) bool {
	return (autoMemoryStore(s, toolName, args) != "" || removesOnlyMemory(s, toolName, args)) && denyRule(toolName, args) == ""
}

// removesOnlyMemory reports whether a Bash call is a plain `rm` whose every
// target is a memory file, checked as a write to it would be.
func removesOnlyMemory(s system.MemoryStores, toolName string, args map[string]any) bool {
	cmd, _ := args["command"].(string)
	files, ok := setting.RemovedFiles(cmd)
	if toolName != tool.ToolBash || !ok {
		return false
	}
	for _, f := range files {
		if autoMemoryStore(s, tool.ToolWrite, map[string]any{"file_path": f}) == "" {
			return false
		}
	}
	return true
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
		notes = append(notes, "Recorded the write time as `modified` in its frontmatter and removed any stray key indentation; the file needs no follow-up edit.")
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

// memoryLine is the turn's memory in one quiet line under the answer, by
// index title and grouped by verb in the order things happened —
// "◇ memory · used Go dev · saved No AI attribution" — cut to width; the
// stores and diffs stay behind ctrl+o. "" when the turn left memory alone.
func memoryLine(entries []conv.MemoryEntry, width int) string {
	var verbs []string
	items := map[string][]string{}
	for _, e := range entries {
		if _, ok := items[e.Verb]; !ok {
			verbs = append(verbs, e.Verb)
		}
		items[e.Verb] = append(items[e.Verb], e.Title)
	}
	if len(verbs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(verbs))
	for _, v := range verbs {
		parts = append(parts, v+" "+strings.Join(items[v], ", "))
	}
	text := strings.Join(parts, " · ")
	if width > 0 {
		text = kit.TruncateText(text, max(8, width-len("  ◇ memory · ")))
	}
	return memoryLabelStyle.Render("◇ memory · ") + memoryItemStyle.Render(text)
}

var (
	memoryLabelStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	memoryItemStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
)
