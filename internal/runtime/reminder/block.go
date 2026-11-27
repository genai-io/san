// Package reminder encodes and identifies system-reminder blocks in message content.
package reminder

import (
	"regexp"
	"slices"
	"strings"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// Standard provider IDs used by the harness. Constants keep ID strings out
// of caller code so renames stay typo-free and refactors are mechanical.
const (
	ProviderSkillsDirectory = "skills-directory"
	ProviderMemoryUser      = "memory-user"
	ProviderMemoryProject   = "memory-project"
	// ProviderMemoryAuto identifies index reminders in older sessions.
	ProviderMemoryAuto = "memory-auto"
	// ProviderMemoryRecall tags selected notes attached to a user message.
	ProviderMemoryRecall = "memory-recall"
	// ProviderGroup is the roster of the group this session joined.
	ProviderGroup = "group"

	SourceSessionStart     = "hook-session-start"
	SourceUserPromptSubmit = "hook-user-prompt-submit"
	SourceRestoredFiles    = "restored-files"
)

// The one spelling of the <system-reminder> tag. Every producer and consumer
// in the tree builds from these — WrapWithSource writes them, Blocks reads
// them through Pattern, and the transcript splitter in internal/session
// compiles Pattern into its own combined regexp. A change here therefore
// reaches every reader; spelling the tag out a second time somewhere else is
// how a reader silently stops recognizing what the writer emits.
const (
	tagOpen        = "<system-reminder"
	tagClose       = "</system-reminder>"
	sourceAttrName = "source"
	quoteEscape    = "&quot;"

	// Pattern matches one whole reminder, capturing the source attribute (empty
	// for a one-time notice) in group 1. Callers that need it inside a larger
	// alternation compile it themselves; Blocks uses it directly.
	Pattern = tagOpen + `(?:\s+` + sourceAttrName + `="([^"]*)")?>.*?` + tagClose
)

// blockRe matches a whole reminder across newlines — bodies are multi-line, and
// the non-greedy body must not run past the first closing tag.
var blockRe = regexp.MustCompile(`(?s)` + Pattern)

var tagEscaper = strings.NewReplacer(tagOpen, "&lt;system-reminder", "</system-reminder", "&lt;/system-reminder")

// Wrap returns body wrapped in <system-reminder>...</system-reminder>. Empty
// body returns "".
func Wrap(body string) string {
	return WrapWithSource(body, "")
}

// HasSystemReminder reports whether text carries a provider-emitted reminder
// (one stamped with a source). One-time notices are unsourced.
func HasSystemReminder(text string) bool {
	return strings.Contains(text, tagOpen+" "+sourceAttrName+"=")
}

// WrapWithSource is like Wrap but stamps a source attribute on the opening
// tag so downstream consumers (transcript parser, viewer) know which
// provider produced the reminder. source="" matches Wrap output exactly to
// keep one-time notice (Enqueue) traffic and tests stable.
func WrapWithSource(body, source string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	// A body that quotes the tag — a restored file of this very code — would
	// otherwise read as a reminder boundary to every parser.
	body = tagEscaper.Replace(body)
	if source == "" {
		return tagOpen + ">\n" + body + "\n" + tagClose
	}
	// Escape any quotes in source defensively; provider IDs are constants
	// today but the surface is public.
	src := strings.ReplaceAll(source, `"`, quoteEscape)
	return tagOpen + " " + sourceAttrName + `="` + src + `">` + "\n" + body + "\n" + tagClose
}

// AppendBlocks preserves the existing blocks and adds each nonempty text separately.
// OpenAI Chat can flatten text-only input, so keep its blank-line boundaries.
func AppendBlocks(content ai.Content, texts ...string) ai.Content {
	cloned, hasText := false, false
	for _, text := range texts {
		if text == "" {
			continue
		}
		if !cloned {
			hasText = slices.ContainsFunc(content, func(b ai.Block) bool { return b.Type == ai.BlockText && b.Text != "" })
			content = slices.Clone(content)
			cloned = true
		}
		if hasText {
			text = "\n\n" + text
		}
		content = append(content, ai.TextBlock(text))
		hasText = true
	}
	return content
}

// Block is one <system-reminder> found inside a message: the provider that
// emitted it (empty for a one-time notice) and the full wrapped text,
// including the tags.
type Block struct {
	Source string
	Text   string
}

// Blocks extracts <system-reminder> tags from a native text block or merged text.
//
// Callers use this to attribute the harness-injected bytes (the skills
// directory, memory files) apart from what the user and the model actually
// said. An unterminated or malformed block simply does not match, so it counts
// as ordinary content rather than being guessed at.
func Blocks(content string) []Block {
	if !strings.Contains(content, tagOpen) {
		return []Block{}
	}
	matches := blockRe.FindAllStringSubmatchIndex(content, -1)
	blocks := make([]Block, 0, len(matches))
	for _, m := range matches {
		source := ""
		if m[2] >= 0 {
			source = strings.ReplaceAll(content[m[2]:m[3]], quoteEscape, `"`)
		}
		blocks = append(blocks, Block{Source: source, Text: content[m[0]:m[1]]})
	}
	return blocks
}
