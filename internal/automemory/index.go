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

// Only a standalone bullet for this single note is owned by the updater.
// Refuse ambiguous entries rather than removing prose or another note's link.
var indexEntry = regexp.MustCompile(`^[-*+] \[([^\[\]]+)\]\((?:\./)?([A-Za-z0-9_-]+\.md)\)(?:[ \t]+([^\[\]<>]*))?$`)

var (
	indexWord = regexp.MustCompile(`[A-Za-z0-9_.-]+`)
	noteLink  = regexp.MustCompile(`\]\((?:\./)?([A-Za-z0-9_-]+\.[mM][dD])\)`)
)

const maxUpdateIndex = 8 * 1024

func (s storeSnapshot) updateIndex() (text string, complete bool) {
	text = s.index.text[:s.updateIndexEnd()]
	if len(text) != len(s.index.text) {
		return text, false
	}
	_, complete = s.updateNoteIDs(len(text))
	return text, complete
}

func (s storeSnapshot) updateIndexEnd() int {
	if len(s.index.text) <= maxUpdateIndex {
		return len(s.index.text)
	}
	// The stored index is UTF-8; cutting after a newline preserves both
	// complete lines and complete characters.
	return strings.LastIndexByte(s.index.text[:maxUpdateIndex], '\n') + 1
}

// Scan the full index for ambiguity: even an omitted line can invalidate an
// earlier ID. Literal blocks are visible as text but do not grant read access.
func (s storeSnapshot) updateNoteIDs(visibleEnd int) (map[string]bool, bool) {
	complete := true
	candidates := map[string]bool{}
	mentions := map[string]int{}
	literal := literalIndexLines(s.index.text)
	offset := 0
	for line := range strings.SplitAfterSeq(s.index.text, "\n") {
		isLiteral := literal[offset]
		offset += len(line)
		if isLiteral {
			continue
		}
		match := indexEntry.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if match != nil && validName(match[2]) && strings.TrimSpace(match[1]) != "" {
			candidates[match[2]] = offset <= visibleEnd
		} else {
			for _, link := range noteLink.FindAllStringSubmatch(line, -1) {
				if validName(strings.ToLower(link[1])) {
					complete = false
					break
				}
			}
		}
		// Another nonliteral mention of a note makes ownership ambiguous.
		seenOnLine := map[string]bool{}
		for _, word := range indexWord.FindAllString(line, -1) {
			name := strings.ToLower(word)
			if !validName(name) || seenOnLine[name] {
				continue
			}
			seenOnLine[name] = true
			mentions[name]++
		}
	}
	for file := range candidates {
		if mentions[strings.ToLower(file)] != 1 {
			complete = false
			delete(candidates, file)
		}
	}
	return candidates, complete
}

// Parse block boundaries, but operate on original lines rather than
// re-rendering Markdown.
func literalIndexLines(text string) map[int]bool {
	literal := map[int]bool{}
	mark := func(offset int) {
		literal[strings.LastIndexByte(text[:offset], '\n')+1] = true
	}
	_ = ast.Walk(goldmark.DefaultParser().Parse(mdtext.NewReader([]byte(text))), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch block := n.(type) {
		case *ast.FencedCodeBlock:
			if block.Info != nil {
				mark(block.Info.Segment.Start)
			}
		case *ast.HTMLBlock:
			if block.HasClosure() {
				mark(block.ClosureLine.Start)
			}
		case *ast.CodeBlock:
		default:
			return ast.WalkContinue, nil
		}
		for i := 0; i < n.Lines().Len(); i++ {
			mark(n.Lines().At(i).Start)
		}
		return ast.WalkContinue, nil
	})
	return literal
}

func editIndex(text string, p proposal) (string, error) {
	mention := regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])` + regexp.QuoteMeta(p.File) + `([^a-z0-9_.-]|$)`)
	literal := literalIndexLines(text)
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
		if entry != nil && entry[2] == p.File {
			if p.Action == actionRemember {
				return "", errors.New("memory index already has an entry for this note")
			}
			continue
		}
		// A case-only spelling might be a separate note or a filesystem
		// alias. Like nonstandard links, leave it for manual editing.
		if mention.MatchString(line) {
			return "", errors.New("ambiguous memory index entry for this note")
		}
		out.WriteString(line)
	}
	if p.Action == actionRemember {
		newline := "\n"
		if strings.Contains(text, "\r\n") {
			newline = "\r\n"
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			out.WriteString(newline)
		}
		entryStart := out.Len()
		fmt.Fprintf(&out, "- [%s](%s) — (%s) %s%s", p.Name, p.File, p.Type, p.Description, newline)
		// Test the actual append: EOF alone can terminate a literal block
		// that would otherwise swallow the new line.
		if literalIndexLines(out.String())[entryStart] {
			return "", errors.New("cannot append memory index entry inside a Markdown literal block")
		}
	}
	if out.Len() > maxIndex {
		return "", errors.New("memory index would exceed size limit")
	}
	return out.String(), nil
}
