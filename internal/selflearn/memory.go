package selflearn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/core/system"
)

// memoryEntryDelimiter separates entries inside a memory file. A standalone
// "§" line lets entries span multiple lines without ambiguity (mirrors the
// hermes memory tool). It is rare enough in prose that substring matching on
// entry bodies stays reliable.
const memoryEntryDelimiter = "\n§\n"

// DefaultMemoryFileCharLimit is the fallback per-file character cap when the
// constructor receives 0. It matches the read-side injection cap
// (system.AutoMemoryByteCap = 25 KB) so a file that fits the budget on write
// also fits when injected — the L1 store must prune/replace rather than grow
// unbounded (see notes/active/l1-background-review.md §4.2 invariant).
const DefaultMemoryFileCharLimit = 25000

// MemoryStore is the project-partitioned, file-backed durable memory written by
// the L1 reviewer fork and read back via system.LoadMemoryFiles. It lives under
// ~/.gen/projects/<encoded-cwd>/memory/ — machine-local, out of the repo, and
// isolated per project (see notes/active/l1-background-review.md §4).
//
// Entries are delimited (memoryEntryDelimiter); add/replace/remove locate an
// entry by a unique substring rather than by index or full text. Writes are
// atomic (temp file + rename) and re-read from disk under the mutex before
// mutating, so an in-flight write never clobbers a concurrent one. Cross-process
// concurrency is best-effort (atomic rename only) — flagged as an open question
// for L2.
// MemoryWriteObserver is invoked after every successful Add / Replace /
// Remove. file is the basename being written ("" ⇒ MEMORY.md index). Used
// by the UI layer to track the current target for the
// §"User-visible surface" "evolving … memory · <topic>" status-bar line.
//
// Contract: SetWriteObserver MUST be called before the first write; the
// reviewer fork is single-flight per session (§6 invariant #8) so we do
// not guard the observer field with a lock.
type MemoryWriteObserver func(file string)

type MemoryStore struct {
	dir     string
	maxFile int // per-file char cap, always > 0 (constructor normalizes)
	onWrite MemoryWriteObserver

	mu sync.Mutex
}

// NewMemoryStore returns the store for cwd's project partition. maxFile is
// the per-file char cap; pass <= 0 for DefaultMemoryFileCharLimit. The
// directory is created lazily on the first write.
func NewMemoryStore(cwd string, maxFile int) *MemoryStore {
	if maxFile <= 0 {
		maxFile = DefaultMemoryFileCharLimit
	}
	return &MemoryStore{dir: system.AutoMemoryDir(cwd), maxFile: maxFile}
}

// SetWriteObserver registers the callback fired after each successful
// write. Must be called before the first write (see type doc).
func (s *MemoryStore) SetWriteObserver(fn MemoryWriteObserver) { s.onWrite = fn }

func (s *MemoryStore) fireWrite(file string) {
	if s.onWrite != nil {
		s.onWrite(file)
	}
}

// Dir is the on-disk directory backing the store.
func (s *MemoryStore) Dir() string { return s.dir }

// resolveFile maps a caller-supplied file name to an absolute path inside the
// store, rejecting traversal and non-markdown names. An empty name defaults to
// the index file.
func (s *MemoryStore) resolveFile(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = system.AutoMemoryIndexName
	}
	if name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid memory file %q: must be a bare file name", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		return "", fmt.Errorf("invalid memory file %q: must end in .md", name)
	}
	return filepath.Join(s.dir, name), nil
}

// Add appends a new entry to file (default index). Exact duplicates are a no-op.
func (s *MemoryStore) Add(file, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("content cannot be empty")
	}
	if err := scanContent(content); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolveFile(file)
	if err != nil {
		return "", err
	}
	entries := readEntries(path)
	if slices.Contains(entries, content) {
		return "Entry already present; nothing added.", nil
	}
	entries = append(entries, content)
	if n := joinedLen(entries); n > s.maxFile {
		return "", fmt.Errorf("entry would put %s at %d/%d chars; replace or remove entries first",
			filepath.Base(path), n, s.maxFile)
	}
	if err := writeEntries(path, entries); err != nil {
		return "", err
	}
	s.fireWrite(file)
	return "Entry added.", nil
}

// Replace swaps the single entry containing oldText for newContent. It errors if
// oldText matches zero or multiple distinct entries.
func (s *MemoryStore) Replace(file, oldText, newContent string) (string, error) {
	oldText = strings.TrimSpace(oldText)
	newContent = strings.TrimSpace(newContent)
	if oldText == "" {
		return "", fmt.Errorf("old_text cannot be empty")
	}
	if newContent == "" {
		return "", fmt.Errorf("content cannot be empty; use remove to delete an entry")
	}
	if err := scanContent(newContent); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolveFile(file)
	if err != nil {
		return "", err
	}
	entries := readEntries(path)
	idx, err := uniqueMatch(entries, oldText)
	if err != nil {
		return "", err
	}
	entries[idx] = newContent
	if n := joinedLen(entries); n > s.maxFile {
		return "", fmt.Errorf("replacement would put %s at %d/%d chars; shorten it or remove other entries",
			filepath.Base(path), n, s.maxFile)
	}
	if err := writeEntries(path, entries); err != nil {
		return "", err
	}
	s.fireWrite(file)
	return "Entry replaced.", nil
}

// Remove deletes the single entry containing oldText.
func (s *MemoryStore) Remove(file, oldText string) (string, error) {
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return "", fmt.Errorf("old_text cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.resolveFile(file)
	if err != nil {
		return "", err
	}
	entries := readEntries(path)
	idx, err := uniqueMatch(entries, oldText)
	if err != nil {
		return "", err
	}
	entries = append(entries[:idx], entries[idx+1:]...)
	if err := writeEntries(path, entries); err != nil {
		return "", err
	}
	s.fireWrite(file)
	return "Entry removed.", nil
}

// uniqueMatch returns the index of the one entry containing sub. Multiple
// distinct matches are an error (ambiguous); identical duplicates resolve to the
// first.
func uniqueMatch(entries []string, sub string) (int, error) {
	first := -1
	distinct := make(map[string]struct{})
	for i, e := range entries {
		if strings.Contains(e, sub) {
			if first == -1 {
				first = i
			}
			distinct[e] = struct{}{}
		}
	}
	if first == -1 {
		return 0, fmt.Errorf("no entry matched %q", sub)
	}
	if len(distinct) > 1 {
		return 0, fmt.Errorf("multiple entries matched %q; be more specific", sub)
	}
	return first, nil
}

func joinedLen(entries []string) int {
	return len(strings.Join(entries, memoryEntryDelimiter))
}

// readEntries parses a memory file into trimmed, non-empty entries. A missing or
// empty file yields no entries.
func readEntries(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return nil
	}
	var out []string
	for _, e := range strings.Split(raw, memoryEntryDelimiter) {
		if t := strings.TrimSpace(e); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// writeEntries persists entries atomically (temp file + rename) so a concurrent
// reader sees either the old or the new complete file, never a truncated one.
// An empty entry list removes the file.
func writeEntries(path string, entries []string) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mem-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	content := strings.Join(entries, memoryEntryDelimiter) + "\n"
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// memoryWriteTool is the L1-only write surface over a MemoryStore. It is granted
// solely to the reviewer fork; the main agent never sees it.
type memoryWriteTool struct {
	store *MemoryStore
}

func newMemoryWriteTool(store *MemoryStore) *memoryWriteTool {
	return &memoryWriteTool{store: store}
}

func (t *memoryWriteTool) Name() string { return "memory_write" }

func (t *memoryWriteTool) Description() string {
	return "Persist a durable fact to project memory (survives across sessions). " +
		"Actions: add (new entry), replace (update — old_text identifies it), remove (delete — old_text identifies it). " +
		"Save user preferences, project conventions, and build/debug insights — never one-off task state or session narratives. " +
		"old_text is a short unique substring of the existing entry. " +
		"file defaults to the MEMORY.md index; spill long detail into a topic file (e.g. debugging.md)."
}

func (t *memoryWriteTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"add", "replace", "remove"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Entry text. Required for add and replace.",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "Short unique substring of the entry to replace or remove.",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Target file name (bare, .md). Defaults to MEMORY.md.",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *memoryWriteTool) Execute(_ context.Context, input map[string]any) (string, error) {
	action := strings.TrimSpace(str(input["action"]))
	file := str(input["file"])
	content := str(input["content"])
	oldText := str(input["old_text"])

	var (
		msg string
		err error
	)
	switch action {
	case "add":
		msg, err = t.store.Add(file, content)
	case "replace":
		msg, err = t.store.Replace(file, oldText, content)
	case "remove":
		msg, err = t.store.Remove(file, oldText)
	default:
		return "", fmt.Errorf("unknown action %q; use add, replace, or remove", action)
	}
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]string{"status": "ok", "message": msg})
	return string(out), nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
