package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/sidecard"
)

// maxRecalled caps the memories attached to one message.
const maxRecalled = 5
const maxRecallCandidates = 60

var (
	recallFilename = regexp.MustCompile(`[\pL\pN_.-]+`)
	recallWord     = regexp.MustCompile(`[\pL\pN]{3,}`)
)

// Rank both stores before cutting the selector's list. Exact filenames win
// over lexical overlap; ties retain the store's deterministic listing order.
func recallCandidates(stores system.MemoryStores, query string) []system.Memory {
	candidates := stores.Memories(0)
	query = strings.ToLower(query)
	filenames, words := map[string]bool{}, map[string]bool{}
	for _, name := range recallFilename.FindAllString(query, -1) {
		filenames[name] = true
		filenames[strings.TrimRight(name, ".")] = true
	}
	for _, word := range recallWord.FindAllString(query, -1) {
		words[word] = true
	}
	type rank struct {
		exact   bool
		overlap int
	}
	ranks := make(map[system.Memory]rank, len(candidates))
	for _, m := range candidates {
		name := strings.ToLower(filepath.Base(m.Path))
		seen := map[string]bool{}
		for _, word := range recallWord.FindAllString(strings.TrimSuffix(name, ".md")+" "+strings.ToLower(m.Description), -1) {
			if words[word] {
				seen[word] = true
			}
		}
		ranks[m] = rank{exact: filenames[name], overlap: len(seen)}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := ranks[candidates[i]], ranks[candidates[j]]
		if a.exact != b.exact {
			return a.exact
		}
		return a.overlap > b.overlap
	})
	return candidates[:min(len(candidates), maxRecallCandidates)]
}

const recallSystemPrompt = `You select memories that will be useful to a coding agent as it handles a user's message. You get the list of memory files, each as <store>/<file> with its description, then the message. A user memory holds in every repository; a project memory holds in the repository the user is working in now.
Return the files the agent needs to handle this message well, up to 5: a rule, fact or preference that bears on what the message asks it to do. Skip one that only shares a word with the message or describes the user in general; when none bears on it, return an empty list.
Reply with JSON only: {"selected_memories": ["<store>/<file>", ...]}`

// Recaller picks the memories a message needs with a side query, so the main
// model has them in hand instead of spending a step reading them.
type Recaller struct {
	Provider  llm.Provider
	Model     string
	Effort    string
	Recording *session.Session
}

func (r Recaller) selectNotes(ctx context.Context, stores system.MemoryStores, query string, recording *session.Completion) ([]system.Memory, error) {
	if ctx.Err() != nil || r.Provider == nil || strings.TrimSpace(query) == "" || stores.Off() {
		return nil, nil
	}
	candidates := recallCandidates(stores, query)
	if len(candidates) == 0 {
		return nil, nil
	}
	byID := map[string]system.Memory{}
	var list strings.Builder
	for _, m := range candidates {
		id := memoryID(m)
		byID[id] = m
		list.WriteString("- " + id + ": " + m.Description + "\n")
	}
	start := time.Now()
	resp, err := sidecard.Complete(ctx, r.Provider, llm.CompletionOptions{
		Model:        r.Model,
		SystemPrompt: recallSystemPrompt,
		Messages: []core.Message{{Role: ai.RoleUser, Content: ai.Content{
			ai.TextBlock("Memory files:\n" + list.String() + "\nUser query:\n"),
			ai.TextBlock(query),
		}}},
		ThinkingEffort: r.Effort,
	}, recording)
	if err != nil {
		log.Logger().Debug("memory recall failed", zap.Int("candidates", len(candidates)), zap.Duration("took", time.Since(start)), zap.Error(err))
		var recordingErr *llm.RecordingError
		if errors.As(err, &recordingErr) {
			return nil, err
		}
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	log.Logger().Debug("memory recall", zap.Int("candidates", len(candidates)), zap.Duration("took", time.Since(start)), zap.String("reply", resp.Content.Text()))
	var reply struct {
		Selected []string `json:"selected_memories"`
	}
	text := resp.Content.Text()
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
	return m.Store + "/" + m.Path[strings.LastIndexAny(m.Path, `/\`)+1:]
}
