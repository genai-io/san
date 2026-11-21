package automemory

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	mdtext "github.com/yuin/goldmark/text"
)

// Only a standalone bullet for this single note is owned by the maintainer.
// Refuse ambiguous entries rather than removing prose or another note's link.
var indexEntry = regexp.MustCompile(`^[-*+] \[[^\[\]]+\]\((?:\./)?([A-Za-z0-9_-]+\.md)\)(?:[ \t]+[^\[\]<>]*)?$`)

func editIndex(text string, p proposal) (string, error) {
	mention := regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])` + regexp.QuoteMeta(p.File) + `([^a-z0-9_.-]|$)`)
	// Parse block boundaries, but splice the original lines so unrelated
	// Markdown (including line endings) isn't re-rendered.
	source := []byte(text)
	literal := map[int]bool{}
	_ = ast.Walk(goldmark.DefaultParser().Parse(mdtext.NewReader(source)), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n.Kind() {
			case ast.KindFencedCodeBlock, ast.KindCodeBlock, ast.KindHTMLBlock:
				for i := 0; i < n.Lines().Len(); i++ {
					segment := n.Lines().At(i)
					start := strings.LastIndexByte(text[:segment.Start], '\n') + 1
					literal[start] = true
				}
			}
		}
		return ast.WalkContinue, nil
	})
	var out strings.Builder
	offset := 0
	for line := range strings.SplitAfterSeq(text, "\n") {
		isLiteral := literal[offset]
		offset += len(line)
		if isLiteral {
			out.WriteString(line)
			continue
		}
		entry := indexEntry.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if entry != nil && entry[1] == p.File {
			if p.Action == "remember" {
				return "", errors.New("memory index already has an entry for this note")
			}
			continue
		}
		// A case-only spelling might be a separate note or a filesystem
		// alias. Like nonstandard links, leave it for manual maintenance.
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
