package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/aside"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
)

// maxRecalled caps the notes returned by one selection.
const maxRecalled = 5

const recallSystemPrompt = `You select memories for the final user's question. The first message lists repository memory filenames and descriptions; the second lists files already in the main context.
Select up to 5 files that add a needed rule, fact, or preference. Do not select files already in context, mere word matches, or general user descriptions. Return an empty list if no new file is needed.
Reply with JSON only: {"selected_memories": ["<filename>", ...]}`

func recallIndex(notes []system.Memory) string {
	var list strings.Builder
	list.WriteString("Memory files:\n")
	for _, note := range notes {
		list.WriteString("- " + memoryID(note) + ": " + note.Description + "\n")
	}
	return list.String()
}

// Recaller picks the memories a message needs with a side query, so the main
// model has them in hand instead of spending a step reading them.
type Recaller struct {
	Provider  llm.Provider
	Model     string
	Effort    string
	Recording *session.Session
	Timeout   time.Duration // Optional wait-mode budget; it does not cancel the main turn.
}

func (r Recaller) selectNotes(ctx context.Context, store system.MemoryStore, query string, recording *session.Completion, covered func(system.Memory) bool) ([]system.Memory, error) {
	if ctx.Err() != nil || r.Provider == nil || strings.TrimSpace(query) == "" || store.Off() {
		return nil, nil
	}
	// Read the current index on each lookup; keep its serialized prefix stable
	// across questions and changes to the already-covered set.
	notes := store.Memories(ctx, 0)
	byID := make(map[string]system.Memory, len(notes))
	var coveredFiles strings.Builder
	for _, note := range notes {
		if ctx.Err() != nil {
			return nil, nil
		}
		id := memoryID(note)
		if covered != nil && covered(note) {
			coveredFiles.WriteString("- " + id + "\n")
		} else {
			byID[id] = note
		}
	}
	if len(byID) == 0 || ctx.Err() != nil {
		return nil, nil
	}
	if coveredFiles.Len() == 0 {
		coveredFiles.WriteString("None.")
	}
	start := time.Now()
	resp, err := aside.Complete(ctx, r.Provider, llm.CompletionOptions{
		Model:        r.Model,
		SystemPrompt: recallSystemPrompt,
		Messages: []core.Message{
			core.UserMessage(recallIndex(notes), nil),
			core.UserMessage("Already in main context:\n"+coveredFiles.String(), nil),
			core.UserMessage(query, nil),
		},
		ThinkingEffort: r.Effort,
	}, recording)
	if err != nil {
		log.Logger().Debug("memory recall failed", zap.Int("candidates", len(byID)), zap.Duration("took", time.Since(start)), zap.Error(err))
		var recordingErr *llm.RecordingError
		if errors.As(err, &recordingErr) {
			return nil, err
		}
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	text := resp.Content.Text()
	log.Logger().Debug("memory recall", zap.Int("candidates", len(byID)), zap.Duration("took", time.Since(start)), zap.String("reply", text))
	var reply struct {
		Selected []string `json:"selected_memories"`
	}
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i < 0 || j < i || json.Unmarshal([]byte(text[i:j+1]), &reply) != nil {
		return nil, nil
	}
	var out []system.Memory
	for _, id := range reply.Selected {
		if m, ok := byID[id]; ok && len(out) < maxRecalled {
			out = append(out, m)
			delete(byID, id)
		}
	}
	return out, nil
}

// LowestEffort is the model's lightest reasoning rung, "" when it has none: a
// pick from a short list needs no thought, and every second of it is waited on.
func LowestEffort(p llm.Provider, store *llm.Store, model *llm.CurrentModelInfo) string {
	if efforts := llm.ThinkingEffortsForModel(p, store, model); len(efforts) > 0 {
		return efforts[0]
	}
	return ""
}

func memoryID(m system.Memory) string {
	return filepath.Base(m.Path)
}
