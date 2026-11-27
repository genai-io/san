package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// AutoMemoryConfig groups the store and recall policy for the main agent.
// An empty store disables Auto Memory; a zero recall mode is Fast.
type AutoMemoryConfig struct {
	Store       system.MemoryStore
	Recall      setting.MemoryRecallConfig
	IndexLimits setting.MemoryPreviewLimits
}

// PreparedMemory holds recall open until the host accepts the enriched input.
type PreparedMemory struct {
	Message    core.Message
	Completion *session.Completion
}

// Prepare recalls notes for the message to be submitted.
func (r Recaller) Prepare(ctx context.Context, store system.MemoryStore, message core.Message, history []core.Message) (PreparedMemory, error) {
	return r.PrepareWithSubmission(ctx, store, message, message, history)
}

// PrepareForAdmission prepares recall without admitting the message. The host
// must pass the completion to main on admission or close it on discard.
func (r Recaller) PrepareForAdmission(ctx context.Context, store system.MemoryStore, message core.Message, history []core.Message) (PreparedMemory, error) {
	return r.Prepare(ctx, store, message, history)
}

// PrepareWithSubmission selects notes using the user input and attaches them
// to the message that will actually be submitted.
func (r Recaller) PrepareWithSubmission(ctx context.Context, store system.MemoryStore, userInput, submission core.Message, history []core.Message) (prepared PreparedMemory, err error) {
	prepared.Message = submission
	query := userInput.Text()
	if store.Off() || r.Provider == nil || strings.TrimSpace(query) == "" {
		return prepared, ctx.Err()
	}
	lookup := ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		lookup, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	existing := recallBlocks(history)
	addRecallBlocks(existing, submission)
	notes, scope, err := r.selectNotes(lookup, store, query, func(note system.Memory) bool {
		return existing.covers(store, note)
	})
	defer func() {
		if prepared.Completion == nil {
			if closeErr := scope.Close(); closeErr != nil {
				if ctx.Err() == nil && lookup.Err() == context.DeadlineExceeded && errors.Is(closeErr, context.DeadlineExceeded) {
					return // The optional wait expired before recall could be recorded.
				}
				err = errors.Join(err, &llm.RecordingError{Err: closeErr})
			}
		}
	}()
	if err != nil {
		if ctx.Err() == nil && lookup.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) {
			return prepared, nil
		}
		return prepared, err
	}
	if ctx.Err() != nil {
		return prepared, ctx.Err()
	}
	if lookup.Err() != nil || len(notes) == 0 {
		return prepared, nil
	}
	var blocks []string
	for _, note := range notes {
		if block := recallBlock(recalledNote(store, note)); block != "" && !existing[memoryID(note)][block] {
			blocks = append(blocks, block)
		}
	}
	if ctx.Err() != nil {
		return prepared, ctx.Err()
	}
	if len(blocks) > 0 {
		prepared.Message.Content = reminder.AppendBlocks(submission.Content, blocks...)
		prepared.Completion = scope
	}
	return prepared, nil
}

const recallNotePrefix = "Relevant memory "

type activeRecallBlocks map[string]map[string]bool

func (r activeRecallBlocks) covers(store system.MemoryStore, note system.Memory) bool {
	blocks := r[memoryID(note)]
	return len(blocks) > 0 && blocks[recallBlock(recalledNote(store, note))]
}

func recallBlocks(messages []core.Message) activeRecallBlocks {
	out := make(activeRecallBlocks)
	for _, message := range messages {
		addRecallBlocks(out, message)
	}
	return out
}

// Only Wait writes note blocks into native messages. Compare the full block
// against a fresh read so an edited note remains eligible for selection.
func addRecallBlocks(out activeRecallBlocks, message core.Message) {
	if message.Role != ai.RoleUser || message.Content.Has(ai.BlockToolResult) {
		return
	}
	for _, content := range message.Content {
		if content.Type != ai.BlockText {
			continue
		}
		for _, block := range reminder.Blocks(content.Text) {
			if block.Source != reminder.ProviderMemoryRecall {
				continue
			}
			_, body, _ := strings.Cut(block.Text, "\n")
			header, _, _ := strings.Cut(body, "\n")
			key, ok := strings.CutPrefix(header, recallNotePrefix)
			end := strings.LastIndex(key, " (saved ")
			if !ok || end < 0 {
				continue
			}
			id := key[:end]
			if out[id] == nil {
				out[id] = make(map[string]bool)
			}
			out[id][block.Text] = true
		}
	}
}

func recallBlock(body string) string {
	return reminder.WrapWithSource(body, reminder.ProviderMemoryRecall)
}

func recalledNote(store system.MemoryStore, note system.Memory) string {
	if !store.Contains(note.Path) {
		return ""
	}
	root, err := os.OpenRoot(filepath.Dir(note.Path))
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(note.Path)
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	f, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	const maxBytes = 2048
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) == 0 {
		return ""
	}
	body := strings.TrimSpace(strings.ToValidUTF8(string(data[:min(len(data), maxBytes)]), "�"))
	if len(data) > maxBytes {
		body += "\n[Note preview truncated.]"
	}
	return fmt.Sprintf("%s%s (saved %s):\n%s", recallNotePrefix, memoryID(note), info.ModTime().Format(time.DateOnly), body)
}
