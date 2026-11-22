package agent

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
)

// fastRecall belongs to one runtime. Only a later model request can consume a
// completed lookup; the worker never modifies conversation history.
type fastRecall struct {
	ctx      context.Context
	stores   system.MemoryStores
	recaller Recaller

	mu                sync.Mutex
	pending           map[string]string
	current, previous *recallJob
	tail              string // Frozen for retries of the same inference.
}

type recallJob struct {
	id, query string
	notes     []system.Memory // Selected references; never cache their text across requests.
	cancel    context.CancelFunc
}

func (r *fastRecall) requestContext() func([]core.Message, bool) []core.Message {
	if r == nil {
		return nil
	}
	return r.prepare
}

func (r *fastRecall) enqueue(id, query string) {
	if r == nil || strings.TrimSpace(query) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = make(map[string]string)
	}
	r.pending[id] = query
}

func (r *fastRecall) cancel() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		r.current.cancel()
	}
	if r.previous != nil {
		r.previous.cancel()
	}
	r.current, r.previous, r.pending, r.tail = nil, nil, nil, ""
}

func (r *fastRecall) prepare(messages []core.Message, retry bool) []core.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if retry {
		return appendRecallTail(messages, r.tail)
	}
	var opened bool
	for i := len(messages) - 1; i >= 0; i-- {
		id := messages[i].ID
		if query, ok := r.pending[id]; ok {
			if r.previous != nil {
				r.previous.cancel()
			}
			r.previous = r.current
			for _, m := range messages[:i+1] {
				delete(r.pending, m.ID)
			}
			ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
			r.current = &recallJob{id: id, query: query, cancel: cancel}
			go r.lookup(ctx, r.current)
			opened = true
			break
		}
		if r.current != nil && id == r.current.id {
			break
		}
	}
	var tail []string
	for _, job := range []*recallJob{r.current, r.previous} {
		if job == nil || (opened && job == r.current) || len(job.notes) == 0 ||
			!slices.ContainsFunc(messages, func(m core.Message) bool { return m.ID == job.id }) {
			continue
		}
		// Read only selected notes, from disk, when forming a fresh request.
		var blocks []string
		for _, note := range job.notes {
			if block := recalledNote(r.stores, note); block != "" {
				blocks = append(blocks, block)
			}
		}
		blocks = newRecallBlocks(messages, blocks)
		blocks = slices.DeleteFunc(blocks, func(block string) bool { return slices.Contains(tail, block) })
		if len(blocks) == 0 {
			continue
		}
		// Current results win if both lookups selected the same note.
		if job == r.previous {
			tail = append(tail, reminder.WrapWithSource("These memories were recalled for the previous user question. Use them only if relevant to the current question; otherwise ignore them.", reminder.ProviderMemoryRecall))
		}
		tail = append(tail, blocks...)
	}
	r.tail = strings.Join(tail, "\n\n")
	return appendRecallTail(messages, r.tail)
}

func appendRecallTail(messages []core.Message, tail string) []core.Message {
	if tail == "" {
		return messages
	}
	return append(slices.Clone(messages), core.UserMessage(tail, nil))
}

func (r *fastRecall) lookup(ctx context.Context, job *recallJob) {
	defer job.cancel()
	scope := r.recaller.Recording.Completion(session.PurposeRecall)
	notes, err := r.recaller.selectNotes(ctx, r.stores, job.query, scope)
	if closeErr := scope.Close(); err != nil || closeErr != nil || ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() == nil && (r.current == job || r.previous == job) {
		job.notes = notes
	}
}
