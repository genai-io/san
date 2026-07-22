package conv

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

// RenderFileDiff renders parsed unified-diff lines in the transcript's diff
// style: a dim line-number gutter, -/+ markers, red/green row backgrounds
// spanning the full row, and a stronger background on the span that actually
// changed when a removed/added pair differs only in part of the line. It is
// shared by the inline Edit/Write result and the approval preview.
//
// maxVisible caps the number of rendered rows (0 means no cap). The second
// return value is how many diff rows were hidden by that cap.
func RenderFileDiff(lines []perm.DiffLine, width, maxVisible int) (string, int) {
	if len(lines) == 0 {
		return "", 0
	}

	const indent = "     "
	rowWidth := max(width-lipgloss.Width(indent), 20)
	gutterWidth := diffGutterWidth(lines)
	emphasis := computeDiffEmphasis(lines)

	lineNoStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDisabled)
	contextStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	noticeStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Italic(true)
	removedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Background(kit.CurrentTheme.ErrorBg)
	removedStrongStyle := removedStyle.Background(kit.CurrentTheme.ErrorBgStrong)
	addedStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success).Background(kit.CurrentTheme.SuccessBg)
	addedStrongStyle := addedStyle.Background(kit.CurrentTheme.SuccessBgStrong)

	var sb strings.Builder
	rendered, hunksSeen := 0, 0
	hidden := 0
	for i, line := range lines {
		if line.Type == perm.DiffLineHunk {
			hunksSeen++
			// The first hunk needs no separator; later ones get a dim "⋯" so
			// the jump between file regions is visible.
			if hunksSeen > 1 && (maxVisible <= 0 || rendered < maxVisible) {
				sb.WriteString(indent + noticeStyle.Render("⋯") + "\n")
			}
			continue
		}
		if maxVisible > 0 && rendered >= maxVisible {
			hidden++
			continue
		}
		rendered++

		switch line.Type {
		case perm.DiffLineMetadata:
			sb.WriteString(indent + noticeStyle.Render(line.Content) + "\n")
		case perm.DiffLineRemoved:
			prefix := fmt.Sprintf("%*d - ", gutterWidth, line.OldLineNo)
			sb.WriteString(indent + renderDiffRow(prefix, line.Content, emphasis[i], removedStyle, removedStrongStyle, rowWidth) + "\n")
		case perm.DiffLineAdded:
			prefix := fmt.Sprintf("%*d + ", gutterWidth, line.NewLineNo)
			sb.WriteString(indent + renderDiffRow(prefix, line.Content, emphasis[i], addedStyle, addedStrongStyle, rowWidth) + "\n")
		default:
			no := line.NewLineNo
			if no == 0 {
				no = line.OldLineNo
			}
			prefix := fmt.Sprintf("%*d   ", gutterWidth, no)
			content := diffDisplayText(prefix + line.Content)
			sb.WriteString(indent + lineNoStyle.Render(prefix) + contextStyle.Render(diffTruncate(content[len(prefix):], rowWidth-runewidth.StringWidth(prefix))) + "\n")
		}
	}
	return sb.String(), hidden
}

// diffGutterWidth sizes the line-number column to the widest number shown.
func diffGutterWidth(lines []perm.DiffLine) int {
	maxNo := 0
	for _, line := range lines {
		if line.OldLineNo > maxNo {
			maxNo = line.OldLineNo
		}
		if line.NewLineNo > maxNo {
			maxNo = line.NewLineNo
		}
	}
	return max(len(fmt.Sprintf("%d", maxNo)), 4)
}

// runeSpan is a [from,to) range in rune positions.
type runeSpan struct {
	from, to int
}

// computeDiffEmphasis pairs each contiguous run of removed lines with the
// added run that follows it (index-wise, like git's word diff) and, for each
// pair that still shares a meaningful prefix and suffix, records the changed
// middle of both lines. Lines without an entry render with the plain row
// background.
func computeDiffEmphasis(lines []perm.DiffLine) map[int]runeSpan {
	emphasis := map[int]runeSpan{}
	i := 0
	for i < len(lines) {
		if lines[i].Type != perm.DiffLineRemoved {
			i++
			continue
		}
		removedStart := i
		for i < len(lines) && lines[i].Type == perm.DiffLineRemoved {
			i++
		}
		addedStart := i
		for i < len(lines) && lines[i].Type == perm.DiffLineAdded {
			i++
		}
		pairs := min(addedStart-removedStart, i-addedStart)
		for k := range pairs {
			oldSpan, newSpan, ok := changedSpan(lines[removedStart+k].Content, lines[addedStart+k].Content)
			if ok {
				emphasis[removedStart+k] = oldSpan
				emphasis[addedStart+k] = newSpan
			}
		}
	}
	return emphasis
}

// changedSpan locates the differing middle of two similar lines via their
// common rune prefix and suffix. It reports ok=false when the lines share
// too little (under a third of the longer line) — a full rewrite reads
// better as a plainly-colored row than as one giant emphasized span.
func changedSpan(oldLine, newLine string) (oldSpan, newSpan runeSpan, ok bool) {
	oldRunes, newRunes := []rune(oldLine), []rune(newLine)
	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldRunes)-prefix && suffix < len(newRunes)-prefix &&
		oldRunes[len(oldRunes)-1-suffix] == newRunes[len(newRunes)-1-suffix] {
		suffix++
	}
	longer := max(len(oldRunes), len(newRunes))
	if longer == 0 || (prefix+suffix)*3 < longer {
		return runeSpan{}, runeSpan{}, false
	}
	return runeSpan{from: prefix, to: len(oldRunes) - suffix}, runeSpan{from: prefix, to: len(newRunes) - suffix}, true
}

// renderDiffRow draws one added/removed row: gutter+marker prefix, content
// with an optional emphasized span, padded with the row background to the
// full row width. The emphasis span is given in content rune positions.
func renderDiffRow(prefix, content string, emphasis runeSpan, base, strong lipgloss.Style, rowWidth int) string {
	prefixRunes := len([]rune(prefix))
	full := []rune(diffDisplayText(prefix + content))
	span := runeSpan{}
	if emphasis.to > emphasis.from {
		// diffDisplayText only rewrites tabs, and content after the prefix
		// keeps its rune positions relative to the expanded prefix — but tab
		// expansion inside the content shifts later runes, so recompute the
		// span against the expanded text.
		span = expandSpan(content, emphasis)
		span.from += prefixRunes
		span.to += prefixRunes
	}

	// Truncate to the row, clamping the emphasis span with it.
	if runewidth.StringWidth(string(full)) > rowWidth {
		full = []rune(runewidth.Truncate(string(full), rowWidth-1, "…"))
		if span.from > len(full) {
			span.from = len(full)
		}
		if span.to > len(full) {
			span.to = len(full)
		}
	}
	if span.to <= span.from {
		span = runeSpan{}
	}

	var sb strings.Builder
	if span.to > span.from {
		sb.WriteString(base.Render(string(full[:span.from])))
		sb.WriteString(strong.Render(string(full[span.from:span.to])))
		sb.WriteString(base.Render(string(full[span.to:])))
	} else {
		sb.WriteString(base.Render(string(full)))
	}
	if pad := rowWidth - runewidth.StringWidth(string(full)); pad > 0 {
		sb.WriteString(base.Render(strings.Repeat(" ", pad)))
	}
	return sb.String()
}

// diffTabReplacement stands in for tabs in rendered diff rows: terminals
// advance tabs to their own stops, which would break background padding.
const diffTabReplacement = "    "

func diffDisplayText(s string) string {
	return strings.ReplaceAll(s, "\t", diffTabReplacement)
}

// expandSpan maps a rune span on raw content to the same span on the
// tab-expanded content.
func expandSpan(content string, span runeSpan) runeSpan {
	runes := []rune(content)
	expandedAt := func(pos int) int {
		if pos > len(runes) {
			pos = len(runes)
		}
		expanded := pos
		for _, r := range runes[:pos] {
			if r == '\t' {
				expanded += len(diffTabReplacement) - 1
			}
		}
		return expanded
	}
	return runeSpan{from: expandedAt(span.from), to: expandedAt(span.to)}
}

func diffTruncate(content string, width int) string {
	if width < 1 {
		return ""
	}
	if runewidth.StringWidth(content) <= width {
		return content
	}
	return runewidth.Truncate(content, width-1, "…")
}
