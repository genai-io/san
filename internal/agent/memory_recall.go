package agent

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/tool/fs"
)

// maxRecalled caps the memories attached to one message, as Claude Code does.
const maxRecalled = 5

// recallDeadline bounds a pick that never answers; the turn never waits on it.
const recallDeadline = time.Minute

const recallSystemPrompt = `You select memories that will be useful to a coding agent as it handles a user's message. You get the list of memory files, each with its store and description, then the message.
Return the files that will clearly help with this message, up to 5. Include one only when you are certain from its description that it bears on the message; when none clearly does, return an empty list.
Reply with JSON only: {"selected_memories": ["<store>/<file>", ...]}`

// Recaller picks the memories a message needs with a side query, so the main
// model has them in hand instead of spending a step reading them.
type Recaller struct {
	Provider llm.Provider
	Model    string
	Effort   string
}

// Recall returns up to maxRecalled memories from stores relevant to query,
// skipping those already surfaced. It returns nothing when there is nothing
// to choose from, the selector fails, or ctx ends first.
func (r Recaller) Recall(ctx context.Context, stores system.MemoryStores, query string, surfaced map[string]bool) []system.Memory {
	var candidates []system.Memory
	for _, m := range stores.Memories() {
		if !surfaced[m.Path] {
			candidates = append(candidates, m)
		}
	}
	if r.Provider == nil || len(candidates) == 0 || strings.TrimSpace(query) == "" {
		return nil
	}
	byID := map[string]system.Memory{}
	var list strings.Builder
	for _, m := range candidates {
		id := memoryID(m)
		byID[id] = m
		list.WriteString("- " + id + ": " + m.Description + "\n")
	}
	start := time.Now()
	resp, err := llm.Complete(ctx, r.Provider, llm.CompletionOptions{
		Model:          r.Model,
		SystemPrompt:   recallSystemPrompt,
		Messages:       []core.Message{core.UserMessage("Memory files:\n"+list.String()+"\nMessage:\n"+query, nil)},
		ThinkingEffort: r.Effort,
	})
	if err != nil {
		log.Logger().Debug("memory recall failed", zap.Int("candidates", len(candidates)), zap.Duration("took", time.Since(start)), zap.Error(err))
		return nil
	}
	log.Logger().Debug("memory recall", zap.Int("candidates", len(candidates)), zap.Duration("took", time.Since(start)), zap.String("reply", resp.Content.Text()))
	var reply struct {
		Selected []string `json:"selected_memories"`
	}
	text := resp.Content.Text()
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i < 0 || j < i || json.Unmarshal([]byte(text[i:j+1]), &reply) != nil {
		return nil
	}
	var out []system.Memory
	for _, id := range reply.Selected {
		if m, ok := byID[id]; ok && len(out) < maxRecalled {
			out = append(out, m)
			delete(byID, id)
		}
	}
	return out
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

// Recall is one message's pick, made beside its turn so the turn never waits:
// it rides the first tool result that completes after it lands, as Claude
// Code's prefetch does, and a turn that calls no tool goes without it.
type Recall struct {
	ready    chan struct{}
	memories []system.Memory
	taken    atomic.Bool
	attached atomic.Pointer[[]string]
	cancel   context.CancelFunc
}

// Start begins picking query's memories, nil when there is nothing to pick.
func (r Recaller) Start(stores system.MemoryStores, query string, surfaced map[string]bool) *Recall {
	if r.Provider == nil || strings.TrimSpace(query) == "" || len(stores.Memories()) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), recallDeadline)
	p := &Recall{ready: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(p.ready)
		p.memories = r.Recall(ctx, stores, query, surfaced)
	}()
	return p
}

// Take returns the pick's reminder once, "" while it is still running.
func (p *Recall) Take() string {
	if p == nil {
		return ""
	}
	select {
	case <-p.ready:
	default:
		return ""
	}
	if p.taken.Swap(true) {
		return ""
	}
	text, paths := recallReminder(p.memories)
	p.attached.Store(&paths)
	return text
}

// Attached lists the memories the pick delivered, nil before it rode a result.
func (p *Recall) Attached() []string {
	if p == nil {
		return nil
	}
	if paths := p.attached.Load(); paths != nil {
		return *paths
	}
	return nil
}

// Stop abandons a pick its turn no longer needs.
func (p *Recall) Stop() {
	if p != nil {
		p.cancel()
	}
}

// WithRecall attaches the current pick, once it lands, to the next tool result
// the model is told about.
func WithRecall(next core.ResultFilter, current func() *Recall) core.ResultFilter {
	return func(ctx context.Context, c sdkagent.PostToolContext) (*sdkagent.Result, error) {
		var out *sdkagent.Result
		if next != nil {
			r, err := next(ctx, c)
			if err != nil {
				return r, err
			}
			out = r
		}
		text := current().Take()
		if text == "" {
			return out, nil
		}
		if out == nil {
			r := c.Result
			out = &r
		}
		out.Content = append(slices.Clone(out.Content), ai.TextBlock(text))
		return out, nil
	}
}

// recallReminder carries the recalled memories' contents, each dated by its
// file's mtime and skipped when the model already has it in view. Each counts
// as read, so the model can edit one without reading it again. It also
// returns the paths it carried.
func recallReminder(memories []system.Memory) (string, []string) {
	var b strings.Builder
	var paths []string
	for _, m := range memories {
		info, err := os.Stat(m.Path)
		if err != nil || fs.Viewed(m.Path) {
			continue
		}
		data, err := os.ReadFile(m.Path)
		if err != nil {
			continue
		}
		fs.RecordFileWritten(m.Path)
		paths = append(paths, m.Path)
		b.WriteString("Memory `" + m.Path + "` (" + m.Store + " store, saved " + info.ModTime().Format(time.DateOnly) + "):\n" + strings.TrimSpace(string(data)) + "\n\n")
	}
	if b.Len() == 0 {
		return "", nil
	}
	return reminder.WrapWithSource("Memories relevant to this message:\n\n"+strings.TrimSpace(b.String()), reminder.ProviderMemoryRecall), paths
}
