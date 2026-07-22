package fs

import (
	"fmt"
	"strings"
)

// Tolerant matching for Edit: when oldText has no exact match, the dominant
// real-world cause is a whitespace transcription slip — the model dropped or
// converted a tab while copying from Read output. Exact matching stays the
// primary path; these fallbacks only run on a zero-match result, and they
// only ever match whole lines.
//
// The ladder has two rungs:
//  1. trailing-whitespace-insensitive — safe to apply automatically, since
//     leading indentation (what ends up in the file) was copied correctly;
//  2. fully whitespace-insensitive — located but NOT applied, because the
//     provided newText carries the same broken indentation and applying it
//     would write that indentation into the file. Instead the error echoes
//     the file's actual lines so the next attempt can copy exact bytes.

// lineSpan is the [start,end) byte range of one line in the file content,
// where end includes the trailing newline when the line has one.
type lineSpan struct {
	start, end int
}

func splitLineSpans(content string) []lineSpan {
	spans := make([]lineSpan, 0, 64)
	lineStart := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			spans = append(spans, lineSpan{start: lineStart, end: i + 1})
			lineStart = i + 1
		}
	}
	if lineStart < len(content) {
		spans = append(spans, lineSpan{start: lineStart, end: len(content)})
	}
	return spans
}

// tolerantMatch is one place where oldText's lines matched consecutive whole
// file lines under a trim function. start/end are byte offsets of the file's
// original text for that region; firstLine/lineCount address the same region
// in line terms (firstLine is 0-based).
type tolerantMatch struct {
	start, end int
	firstLine  int
	lineCount  int
}

// resolveTolerantMatch runs the fallback ladder for one edit whose oldText
// had no exact match. It returns a range to apply (trailing-whitespace rung)
// or an error describing the closest the file gets to oldText.
func resolveTolerantMatch(content string, spans []lineSpan, editIndex int, edit editReplacement) (editRange, error) {
	oldLines, matchTrailingNewline := splitOldTextLines(edit.oldString)

	// Whitespace-only oldText would "match" any run of blank lines under a
	// trim; exact matching is the only meaningful mode for it.
	if allBlank(oldLines) {
		return editRange{}, notFoundError(editIndex)
	}

	trimTrailing := func(s string) string { return strings.TrimRight(s, " \t") }
	matches := matchTrimmedLines(content, spans, oldLines, matchTrailingNewline, trimTrailing)
	switch len(matches) {
	case 1:
		m := matches[0]
		return editRange{start: m.start, end: m.end, replacement: edit.newString, editIndex: editIndex}, nil
	case 0:
		// Fall through to the diagnostic rung.
	default:
		return editRange{}, fmt.Errorf("edits[%d]: oldText matches %d locations when ignoring trailing whitespace; include more surrounding context", editIndex, len(matches))
	}

	matches = matchTrimmedLines(content, spans, oldLines, matchTrailingNewline, strings.TrimSpace)
	if len(matches) == 1 {
		m := matches[0]
		return editRange{}, fmt.Errorf(
			"edits[%d]: oldText does not match the file exactly, but lines %d-%d match it when indentation is ignored — the indentation in oldText is wrong. The file's actual text is (format: line number, tab, exact content):\n%sRetry with the indentation exactly as shown after the tab, in both oldText and newText.",
			editIndex, m.firstLine+1, m.firstLine+m.lineCount, echoFileLines(content, spans, m.firstLine, m.lineCount))
	}
	return editRange{}, notFoundError(editIndex)
}

func notFoundError(editIndex int) error {
	return fmt.Errorf("edits[%d]: oldText was not found. The file appears unchanged since it was last read (same size and mtime), so oldText likely has a transcription slip. Re-read the target lines and copy them exactly", editIndex)
}

// splitOldTextLines splits oldText for line-based matching. A trailing
// newline is not a line of its own; it means the matched region must keep
// the last line's newline.
func splitOldTextLines(oldText string) (lines []string, matchTrailingNewline bool) {
	lines = strings.Split(oldText, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1], true
	}
	return lines, false
}

func allBlank(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

// matchTrimmedLines finds every place where oldLines appear as consecutive
// whole file lines when both sides are viewed through trimLine. The returned
// byte ranges cover the file's original text, so a replacement drops the
// file's own whitespace along with the rest of the matched region.
func matchTrimmedLines(content string, spans []lineSpan, oldLines []string, matchTrailingNewline bool, trimLine func(string) string) []tolerantMatch {
	var matches []tolerantMatch
	for i := 0; i+len(oldLines) <= len(spans); i++ {
		if !trimmedLinesEqual(content, spans[i:i+len(oldLines)], oldLines, trimLine) {
			continue
		}
		last := spans[i+len(oldLines)-1]
		end := last.end
		if !matchTrailingNewline && end > last.start && content[end-1] == '\n' {
			end--
		}
		matches = append(matches, tolerantMatch{start: spans[i].start, end: end, firstLine: i, lineCount: len(oldLines)})
	}
	return matches
}

func trimmedLinesEqual(content string, spans []lineSpan, oldLines []string, trimLine func(string) string) bool {
	for j, span := range spans {
		fileLine := strings.TrimSuffix(content[span.start:span.end], "\n")
		if trimLine(fileLine) != trimLine(oldLines[j]) {
			return false
		}
	}
	return true
}

// echoFileLines renders a region of the file in the same "line number, tab,
// content" format Read uses, so the caller of a failed edit can copy the
// exact bytes without another Read round-trip.
func echoFileLines(content string, spans []lineSpan, firstLine, lineCount int) string {
	const maxEchoLines = 30
	var sb strings.Builder
	for j := 0; j < lineCount && j < maxEchoLines; j++ {
		span := spans[firstLine+j]
		fmt.Fprintf(&sb, "%6d\t%s\n", firstLine+j+1, strings.TrimSuffix(content[span.start:span.end], "\n"))
	}
	if lineCount > maxEchoLines {
		fmt.Fprintf(&sb, "... (%d more lines; Read the file for the rest)\n", lineCount-maxEchoLines)
	}
	return sb.String()
}
