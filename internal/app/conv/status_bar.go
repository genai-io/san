// Package conv: status-bar components — context bar, color tiers,
// compressions badge, and the responsive segment allocator. Pure
// functions over primitives; the orchestrator (RenderModeStatus) wires
// them to env state.
package conv

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// Threshold percentages for the 4 PRD §7.2 color tiers.
const (
	pctGood     = 50.0
	pctWarn     = 80.0
	pctCritical = 95.0

	// contextBarWidth is the cell count for the visual bar (PRD §7.1).
	contextBarWidth = 10
)

// contextTier classifies a context-window fill percentage into one of
// the 4 PRD §7.2 tiers. Off-by-one preserved: 80 itself falls into warn,
// only strictly-greater-than-80 is bad. ≥95 is critical.
type contextTier int

const (
	tierNone     contextTier = iota // pct unknown — denominator missing
	tierGood                        // [0, 50]    healthy
	tierWarn                        // (50, 80]   watch
	tierBad                         // (80, 95)   pressure
	tierCritical                    // [95, 100+] imminent compression
)

// classifyContextTier maps a percentage to its tier. Defensive for
// out-of-range inputs: pct < 0 returns tierGood, pct > 100 returns
// tierCritical. Renderers should still clamp for clean display.
func classifyContextTier(pct float64) contextTier {
	switch {
	case pct <= pctGood: // pct ≤ 50
		return tierGood
	case pct <= pctWarn: // 50 < pct ≤ 80
		return tierWarn
	case pct < pctCritical: // 80 < pct < 95
		return tierBad
	default: // pct ≥ 95
		return tierCritical
	}
}

// style resolves a tier to a lipgloss style composed from existing
// theme tokens (per project decision: no new theme infrastructure).
func (t contextTier) style() lipgloss.Style {
	switch t {
	case tierGood:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
	case tierWarn:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	case tierBad:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	case tierCritical:
		// Critical = Error + Bold. Distinct from "bad" without adding a
		// new theme token.
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Bold(true)
	default: // tierNone
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	}
}

// RenderContextBar renders the 10-cell bar with a percentage label:
//
//	"[██████░░░░] 71%"   normal case
//	"[----------] --"    when limit is 0 (unknown)
//
// The percentage is rounded to an integer at this layer (PRD §4.2); the
// engine itself never rounds. `used` is clamped to [0, limit] before
// computing pct so callers cannot accidentally render negatives or >100%.
func RenderContextBar(used, limit int) string {
	if limit <= 0 {
		dim := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
		return dim.Render("[" + strings.Repeat("-", contextBarWidth) + "] --")
	}
	if used < 0 {
		used = 0
	}
	pct := float64(used) / float64(limit) * 100
	if pct > 100 {
		pct = 100
	}
	filled := int((pct/100)*float64(contextBarWidth) + 0.5) // round to nearest cell
	if filled > contextBarWidth {
		filled = contextBarWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", contextBarWidth-filled)
	style := classifyContextTier(pct).style()
	return style.Render(fmt.Sprintf("[%s] %d%%", bar, int(pct+0.5)))
}

// RenderContextLabel renders the "ctx X/Y" segment using compact
// humanized numbers (PRD §7.4). Limit renders as "--" when unknown.
func RenderContextLabel(used, limit int) string {
	muted := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	if limit <= 0 {
		return muted.Render(fmt.Sprintf("ctx %s/--", kit.FormatTokenCount(used)))
	}
	return muted.Render(fmt.Sprintf("ctx %s/%s", kit.FormatTokenCount(used), kit.FormatTokenCount(limit)))
}

// compressionBadgeStyle escalates color with count (PRD §7.5):
//
//	<5     muted
//	5–9    warn
//	≥10    error
func compressionBadgeStyle(n int) lipgloss.Style {
	switch {
	case n >= 10:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
	case n >= 5:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
	default:
		return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	}
}

// RenderCompressionsBadge returns the "🗜️ N" badge or "" when n ≤ 0.
// Color escalates per compressionBadgeStyle.
func RenderCompressionsBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return compressionBadgeStyle(n).Render(fmt.Sprintf("🗜️ %d", n))
}

// segmentSep is the internal join separator. Uses the ASCII Unit
// Separator (\x1f) so it can never collide with rendered segment text
// (lipgloss emits ANSI escapes with \x1b; user strings don't contain
// control characters). Callers split on it to swap in their own visual
// separator.
const segmentSep = "\x1f"

// statusSegment is a unit the allocator can keep or drop atomically.
// Lower priority drops first when width is constrained (PRD §8.3).
type statusSegment struct {
	render   func() string // lazy — only invoked if the segment survives
	width    int           // precomputed visible width (excluding separators)
	priority int           // 1 = highest (drops last); larger = drops first
}

// AllocateStatusSegments walks segments in priority order, keeping each
// segment that fits in the remaining budget plus its leading separator.
// Segments are never truncated mid-render. Returns survivors joined with
// segmentSep, in the original caller-supplied order. Callers split on
// segmentSep to rejoin with their preferred visual separator.
func AllocateStatusSegments(segments []statusSegment, availableWidth int) string {
	if availableWidth <= 0 || len(segments) == 0 {
		return ""
	}
	// Sort by priority ascending (1 first). Stable so equal-priority
	// segments keep their original order.
	order := make([]int, len(segments))
	for i := range order {
		order[i] = i
	}
	// Insertion sort — n is tiny (≤6 segments in practice).
	for i := 1; i < len(order); i++ {
		j := i
		for j > 0 && segments[order[j-1]].priority > segments[order[j]].priority {
			order[j-1], order[j] = order[j], order[j-1]
			j--
		}
	}

	type kept struct {
		idx    int
		render string
	}
	var survivors []kept
	budget := availableWidth
	for _, idx := range order {
		s := segments[idx]
		need := s.width
		if len(survivors) > 0 {
			need += len(segmentSep)
		}
		if need > budget {
			continue
		}
		survivors = append(survivors, kept{idx: idx, render: s.render()})
		budget -= need
	}

	// Re-emit in the original (caller-supplied) order so the layout reads
	// naturally regardless of priority.
	byIdx := make(map[int]int, len(survivors))
	for i, k := range survivors {
		byIdx[k.idx] = i
	}
	out := make([]string, 0, len(survivors))
	for origIdx := range segments {
		if i, ok := byIdx[origIdx]; ok {
			out = append(out, survivors[i].render)
		}
	}
	return strings.Join(out, segmentSep)
}
