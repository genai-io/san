package automemory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/genai-io/san/internal/core/system"

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

const maxUpdateIndex = 32 * 1024

// updateIndex returns the index text shown to Update and, when new notes must
// be disabled, the reason.
func (s storeSnapshot) updateIndex() (text, problem string) {
	text = s.index.text[:s.updateIndexEnd()]
	if len(text) != len(s.index.text) {
		return text, fmt.Sprintf("MEMORY.md is over %d KiB", maxUpdateIndex/1024)
	}
	_, problem = s.updateNoteIDs(len(text))
	return text, problem
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
func (s storeSnapshot) updateNoteIDs(visibleEnd int) (map[string]bool, string) {
	var problem string
	candidates := map[string]bool{}
	mentions := map[string]int{}
	literal := literalIndexLines(s.index.text)
	offset := 0
	lineNo := 0
	for line := range strings.SplitAfterSeq(s.index.text, "\n") {
		isLiteral := literal[offset]
		offset += len(line)
		lineNo++
		if isLiteral {
			continue
		}
		match := indexEntry.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
		if match != nil && validName(match[2]) && strings.TrimSpace(match[1]) != "" {
			candidates[match[2]] = offset <= visibleEnd
		} else if problem == "" && linksNote(line) {
			problem = fmt.Sprintf("MEMORY.md line %d links a note outside the `- [Name](file.md) description` form", lineNo)
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
			if problem == "" {
				problem = file + " is mentioned more than once in MEMORY.md"
			}
			delete(candidates, file)
		}
	}
	return candidates, problem
}

func linksNote(line string) bool {
	for _, link := range noteLink.FindAllStringSubmatch(line, -1) {
		if validName(strings.ToLower(link[1])) {
			return true
		}
	}
	return false
}

// mentionsNote reports a note filename, which would make a new index entry
// ambiguous and disable later additions.
func mentionsNote(s string) bool {
	for _, word := range indexWord.FindAllString(s, -1) {
		if validName(strings.ToLower(word)) {
			return true
		}
	}
	return false
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

// Notes lists the notes MEMORY.md links outside literal blocks, in filename
// order, for Recall. An unlinked file is not recalled, because Update can
// neither read nor forget it.
func Notes(ctx context.Context, store system.MemoryStore) []system.Memory {
	if store.Off() || ctx.Err() != nil {
		return nil
	}
	root, err := os.OpenRoot(string(store))
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	index, err := readRevision(root, system.AutoMemoryIndexName, maxIndex)
	if err != nil {
		return nil
	}
	linked := map[string]bool{}
	literal := literalIndexLines(index.text)
	offset := 0
	for line := range strings.SplitAfterSeq(index.text, "\n") {
		isLiteral := literal[offset]
		offset += len(line)
		if isLiteral {
			continue
		}
		for _, link := range noteLink.FindAllStringSubmatch(line, -1) {
			if validName(link[1]) {
				linked[link[1]] = true
			}
		}
	}
	var out []system.Memory
	for _, name := range slices.Sorted(maps.Keys(linked)) {
		if ctx.Err() != nil {
			return out
		}
		if info, err := root.Lstat(name); err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := root.Open(name)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, 2048))
		_ = f.Close()
		if err == nil {
			out = append(out, system.Memory{Path: filepath.Join(string(store), name), Description: system.FrontmatterField(string(data), "description")})
		}
	}
	return out
}
