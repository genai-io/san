package app

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

// autoMemoryStore returns the repository's Auto Memory store when enabled.
func (m *model) autoMemoryStore() system.MemoryStore {
	if !m.services.Setting.AutoMemory() {
		return ""
	}
	return setting.MemoryStoreFor(m.env.CWD)
}

// memoryLine is the turn's memory changes in one quiet line under the answer,
// grouped by verb. Reads and recalls already appear at their tool steps and
// must not be repeated here. An unchanged turn gets no footer.
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
		items[e.Verb] = append(items[e.Verb], memoryItemStyle.Render(e.Title))
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

// The titles carry the line; the icon and verbs recede behind them.
var (
	memoryLabelStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	memoryItemStyle  = lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
)
