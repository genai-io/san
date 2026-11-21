// Package reminder manages <system-reminder> content that the harness
// attaches to user messages.
//
// system-reminder is a content tag the LLM sees inside a user message, but
// treats as a system instruction (not real user input). Compared with the
// system prompt, it is:
//
//   - cache-friendly: lives in immutable conversation history once attached
//   - self-managed: the harness re-injects it on PostCompact
//   - low-churn: state changes append a new tag instead of invalidating
//     the system-prompt cache prefix
//
// Use the reminder package for content that is "session-level" or
// "project-level" and may change during a session — skills directory, memory
// files, one-time notices. Behavior that is true for every San session
// (identity, policy, guidelines) belongs in the system prompt instead.
package reminder

import (
	"regexp"
	"slices"
	"strings"
	"sync"

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
)

// provider is a long-lived reminder source: render produces its body on every
// emission, and "" skips it (e.g. no enabled skills).
type provider struct {
	id     string
	render func() string
}

// Reminder holds the runtime state for reminders.
//
// It tracks conversation context preparation and two reminder collections:
//
//   - providers: long-lived sources that emit once per conversation and
//     again when their content changes (e.g. skills, memory).
//   - pending: reminders queued for the next user message; each entry tracks
//     which provider (if any) emitted it so RequeueSystemReminders can replace
//     stale provider entries instead of duplicating them. Entries with no
//     provider are one-time notices (see Enqueue).
//
// All operations are safe for concurrent use.
type Reminder struct {
	mu        sync.Mutex
	providers []provider
	pending   []pendingEntry
	emitted   map[string]string // latest wrapped version per provider
	prepared  bool
}

// pendingEntry pairs a wrapped <system-reminder> body with the ID of the
// provider that produced it. providerID is empty for one-time notices queued
// via Enqueue (e.g. hook context), so they always coexist with — and never
// shadow — provider emissions.
type pendingEntry struct {
	providerID string // "" for a one-time notice (Enqueue)
	wrapped    string // already wrapped in <system-reminder>
}

// New creates an empty Reminder.
func New() *Reminder {
	return &Reminder{}
}

// Register adds a provider whose output RequeueSystemReminders emits.
// Re-registering an existing id replaces the old provider.
func (s *Reminder) Register(id string, render func() string) {
	p := provider{id: id, render: render}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.providers {
		if existing.id == p.id {
			s.providers[i] = p
			return
		}
	}
	s.providers = append(s.providers, p)
}

// Unregister removes the provider with the given ID. Missing IDs are silently
// ignored.
func (s *Reminder) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.providers {
		if existing.id == id {
			s.providers = slices.Delete(s.providers, i, i+1)
			s.pending = slices.DeleteFunc(s.pending, func(e pendingEntry) bool {
				return e.providerID != "" && e.providerID == id
			})
			s.queueProviderLocked(id, "")
			return
		}
	}
}

// Enqueue adds a one-time notice to the pending queue. A notice is a reminder
// not backed by a provider — e.g. hook context — emitted once and never
// auto-regenerated. The body should not include the <system-reminder>
// wrapper; this method adds it.
//
// Empty bodies are dropped silently. Notices persist independently of
// RequeueSystemReminders — the latter only touches provider-emitted entries.
func (s *Reminder) Enqueue(body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	wrapped := Wrap(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, pendingEntry{wrapped: wrapped})
}

// DiscardPendingNotices drops every pending notice — entries queued via
// Enqueue (providerID==""). Used by /compact, /clear and /resume: the output
// that produced these notices is no longer in the conversation the model
// sees. Provider entries are preserved.
func (s *Reminder) DiscardPendingNotices() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discardPendingNoticesLocked()
}

func (s *Reminder) discardPendingNoticesLocked() {
	if len(s.pending) == 0 {
		return
	}
	kept := s.pending[:0]
	for _, e := range s.pending {
		if e.providerID != "" {
			kept = append(kept, e)
		}
	}
	s.pending = kept
}

// RequeueSystemReminders replaces pending provider versions with current
// renders, skipping exact versions already attached. Notices keep their order.
// A provider that stops rendering content emits a source-scoped removal once.
func (s *Reminder) RequeueSystemReminders() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requeueSystemRemindersLocked()
}

func (s *Reminder) requeueSystemRemindersLocked() {
	if len(s.providers) == 0 {
		return
	}

	knownIDs := make(map[string]struct{}, len(s.providers))
	for _, p := range s.providers {
		knownIDs[p.id] = struct{}{}
	}
	kept := s.pending[:0]
	for _, e := range s.pending {
		if _, isProvider := knownIDs[e.providerID]; e.providerID == "" || !isProvider {
			kept = append(kept, e)
		}
	}
	s.pending = kept

	for _, p := range s.providers {
		s.queueProviderLocked(p.id, p.render())
	}
}

const removalBody = "Earlier reminders from this source no longer apply."

func (s *Reminder) queueProviderLocked(id, body string) {
	wrapped := WrapWithSource(body, id)
	last := s.emitted[id]
	if wrapped == "" && last != "" {
		wrapped = WrapWithSource(removalBody, id)
	}
	if wrapped != "" && wrapped != last {
		s.pending = append(s.pending, pendingEntry{providerID: id, wrapped: wrapped})
	}
}

// Drain returns all pending reminders (already wrapped) and clears the queue.
func (s *Reminder) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainLocked()
}

func (s *Reminder) drainLocked() []string {
	out := s.wrappedLocked()
	for _, e := range s.pending {
		if e.providerID != "" {
			if s.emitted == nil {
				s.emitted = make(map[string]string)
			}
			s.emitted[e.providerID] = e.wrapped
		}
	}
	s.pending = nil
	return out
}

// Pending returns the queued reminders (already wrapped) without clearing the
// queue. Use it to inspect what the next user message will carry; use Drain
// when you are the one attaching them.
func (s *Reminder) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wrappedLocked()
}

// wrappedLocked returns the queue's wire strings. Shared by Drain and Pending
// so the two views of the same queue cannot disagree about what an entry
// renders as. Caller holds s.mu.
func (s *Reminder) wrappedLocked() []string {
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]string, len(s.pending))
	for i, e := range s.pending {
		out[i] = e.wrapped
	}
	return out
}

// Empty reports whether there are no pending reminders.
func (s *Reminder) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending) == 0
}

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
