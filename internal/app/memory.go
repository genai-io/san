package app

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
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

// memoryLine is the turn's memory changes in one quiet line under the answer,
// grouped by verb. Reads and recalls already appear at their tool steps and
// must not be repeated here. A write names its store; an unchanged turn gets
// no footer.
func memoryLine(entries []conv.MemoryEntry, width int) string {
	var verbs []string
	items := map[string][]string{}
	icon := kit.MemoryMark
	for _, e := range entries {
		if e.Verb == "used" || e.Verb == "recalled" {
			continue
		}
		if _, ok := items[e.Verb]; !ok {
			verbs = append(verbs, e.Verb)
		}
		title := memoryItemStyle.Render(e.Title)
		switch e.Verb {
		case "saved":
			title += memoryLabelStyle.Render(" to " + e.Store)
		case "updated":
			title += memoryLabelStyle.Render(" in " + e.Store)
		}
		items[e.Verb] = append(items[e.Verb], title)
	}
	if len(verbs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(verbs))
	for _, v := range verbs {
		parts = append(parts, memoryLabelStyle.Render(v+" ")+strings.Join(items[v], memoryLabelStyle.Render(", ")))
	}
	line := memoryLabelStyle.Render(icon+" ") + strings.Join(parts, memoryLabelStyle.Render(" · "))
	if width > 0 {
		line = kit.TruncateText(line, max(8, width-2))
	}
	return line
}

// The titles carry the line; the icon, verbs and stores recede behind them.
var (
	memoryLabelStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	memoryItemStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)
