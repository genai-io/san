package automemory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Only a standalone bullet for this single note is owned by the maintainer.
// Refuse ambiguous entries rather than removing prose or another note's link.
var indexEntry = regexp.MustCompile(`^[-*+] \[[^\[\]]+\]\((?:\./)?([A-Za-z0-9_-]+\.md)\)(?:[ \t]+[^\[\]<>]*)?$`)

func editIndex(text string, p proposal) (string, error) {
	mention := regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])` + regexp.QuoteMeta(p.File) + `([^a-z0-9_.-]|$)`)
	var out strings.Builder
	for line := range strings.SplitAfterSeq(text, "\n") {
		entry := indexEntry.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if entry != nil && strings.EqualFold(entry[1], p.File) {
			if p.Action == "remember" {
				return "", errors.New("memory index already has an entry for this note")
			}
			continue
		}
		// Nonstandard links (references, inline prose, links with titles,
		// etc.) need manual maintenance; never rewrite their lines.
		if mention.MatchString(line) {
			return "", errors.New("ambiguous memory index entry for this note")
		}
		out.WriteString(line)
	}
	if p.Action == "remember" {
		newline := "\n"
		if strings.Contains(text, "\r\n") {
			newline = "\r\n"
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			out.WriteString(newline)
		}
		fmt.Fprintf(&out, "- [%s](%s) — %s%s", p.Name, p.File, p.Description, newline)
	}
	if out.Len() > maxIndex {
		return "", errors.New("memory index would exceed size limit")
	}
	return out.String(), nil
}
