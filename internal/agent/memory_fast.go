package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// fastRecall belongs to one runtime. Only a later model request can consume a
// completed lookup; the worker never modifies conversation history.
type fastRecall struct {
	ctx      context.Context
	store    system.MemoryStore
	recaller Recaller

	mu                sync.Mutex
	pending           map[string]string
	current, previous *recallJob
	retained          []recallEntry
	batch             uint64
	staged            map[string][]core.Message // Frozen request-only messages for retries.
	shown             map[string]bool           // Notes covered by the last responding request.
}

// MemoryRecalled describes new recall context used by the main request. It
// carries display metadata only, never note bodies or store paths.
type MemoryRecalled struct {
	AfterMessage string
	NoteIDs      []string
}

type recallEntry struct {
	note     system.Memory
	user     string // User input that bounds this note's lifetime.
	after    string // Native request boundary where the note was first attached.
	batch    uint64
	previous bool
}

// Limit carryover by both recent user inputs (not tool steps) and note count.
const (
	maxRecallMessages = 20
	maxRetainedRecall = 2 * maxRecalled
)

type recallJob struct {
	id, query string
	notes     []system.Memory // Selected references; never cache their text across requests.
	cancel    context.CancelFunc
}

func (r *fastRecall) requestContext() func([]core.Message, bool) ([]core.Message, func() core.Event) {
	if r == nil {
		return nil
	}
	return r.prepareRequest
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
	r.current, r.previous, r.pending, r.retained, r.staged = nil, nil, nil, nil, nil
}

func (r *fastRecall) prepareRequest(messages []core.Message, retry bool) ([]core.Message, func() core.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !retry {
		r.stageRequest(messages)
	}
	prepared, inserted := applyRecall(messages, r.staged)
	return prepared, r.notice(inserted)
}

func (r *fastRecall) stageRequest(messages []core.Message) {
	var opened *recallJob
	var lookup context.Context
	for i := len(messages) - 1; i >= 0 && len(r.pending) > 0; i-- {
		id := messages[i].ID
		if query, ok := r.pending[id]; ok {
			if r.previous != nil {
				r.previous.cancel()
			}
			r.previous = r.current
			for _, m := range messages[:i+1] {
				delete(r.pending, m.ID)
			}
			var cancel context.CancelFunc
			lookup, cancel = context.WithTimeout(r.ctx, 30*time.Second)
			opened = &recallJob{id: id, query: query, cancel: cancel}
			r.current = opened
			break
		}
		if r.current != nil && id == r.current.id {
			break
		}
	}
	if opened == nil && len(r.retained) == 0 && !r.current.ready() && !r.previous.ready() {
		r.staged = nil
		return
	}
	existing := recallBlocks(messages)
	covered := r.stage(messages, existing)
	if opened != nil {
		go r.lookup(lookup, opened, func(note system.Memory) bool {
			return covered[memoryID(note)] || existing.covers(r.store, note)
		})
	}
}

func (j *recallJob) ready() bool { return j != nil && len(j.notes) > 0 }

// Only ordinary user input counts toward the carryover window.
func recallAnchor(msg core.Message) bool {
	return msg.ID != "" && msg.Role == ai.RoleUser && !msg.Content.Has(ai.BlockToolCall) && !msg.Content.Has(ai.BlockToolResult) &&
		(msg.Content.Has(ai.BlockText) || msg.Content.Has(ai.BlockImage))
}

// A recall message cannot interrupt an assistant tool call or a run of results.
func recallBoundary(messages []core.Message, i int) bool {
	msg := messages[i]
	return msg.ID != "" && !msg.Content.Has(ai.BlockToolCall) &&
		(i+1 == len(messages) || !messages[i+1].Content.Has(ai.BlockToolResult))
}

func (r *fastRecall) stage(messages []core.Message, existing activeRecallBlocks) map[string]bool {
	anchors := make(map[string]bool)
	boundaries := make(map[string]bool, len(r.retained)+1)
	for _, entry := range r.retained {
		boundaries[entry.after] = false
	}
	var latest string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if _, needed := boundaries[msg.ID]; needed && recallBoundary(messages, i) {
			boundaries[msg.ID] = true
		}
		if len(anchors) < maxRecallMessages && recallAnchor(msg) {
			if latest == "" {
				latest = msg.ID
			}
			anchors[msg.ID] = true
		}
	}
	// Keep each batch where it first appeared. Append new batches after the
	// native tail; never edit a previously sent user message to add a note.
	type rendered struct {
		recallEntry
		body string
	}
	var ready []rendered
	seen := make(map[string]bool)
	add := func(entry recallEntry) {
		id := memoryID(entry.note)
		if !anchors[entry.user] || !boundaries[entry.after] || seen[id] {
			return
		}
		body := recalledNote(r.store, entry.note)
		if body == "" || existing[id][recallBlock(body)] {
			return
		}
		seen[id] = true
		ready = append(ready, rendered{entry, body})
	}
	for _, entry := range r.retained {
		add(entry)
	}
	if last := len(messages) - 1; latest != "" && recallBoundary(messages, last) {
		after := messages[last].ID
		boundaries[after] = true
		r.batch++
		// Current results win if both jobs select a note not already in context.
		for _, job := range []*recallJob{r.current, r.previous} {
			if job == nil || !anchors[job.id] {
				continue
			}
			for _, note := range job.notes {
				add(recallEntry{note: note, user: latest, after: after, batch: r.batch, previous: job.id != latest})
			}
			job.notes = nil
		}
	}
	ready = ready[max(0, len(ready)-maxRetainedRecall):]
	r.retained = r.retained[:0]
	r.staged = make(map[string][]core.Message)
	covered := make(map[string]bool, len(ready))
	for len(ready) > 0 {
		first := ready[0]
		var current, previous []string
		for len(ready) > 0 && ready[0].batch == first.batch {
			note := ready[0]
			ready = ready[1:]
			r.retained = append(r.retained, note.recallEntry)
			covered[memoryID(note.note)] = true
			if note.previous {
				previous = append(previous, note.body)
			} else {
				current = append(current, note.body)
			}
		}
		if len(previous) > 0 {
			current = append(current, "Recalled for the previous question; use only if relevant.")
			current = append(current, previous...)
		}
		msg := core.UserMessage(recallBlock(strings.Join(current, "\n\n")), nil)
		r.staged[first.after] = append(r.staged[first.after], msg)
	}
	return covered
}

func (r *fastRecall) notice(inserted map[string]bool) func() core.Event {
	if len(inserted) == 0 && len(r.shown) == 0 {
		return nil
	}
	type deliveredNote struct{ id, after string }
	var delivered []deliveredNote
	active := make(map[string]bool, len(r.retained))
	for _, entry := range r.retained {
		if inserted[entry.after] {
			id := memoryID(entry.note)
			active[id] = true
			delivered = append(delivered, deliveredNote{id, entry.after})
		}
	}
	return func() core.Event {
		r.mu.Lock()
		defer r.mu.Unlock()
		var added []string
		var after string
		for _, note := range delivered {
			if !r.shown[note.id] {
				added = append(added, note.id)
				after = note.after
			}
		}
		r.shown = active
		if len(added) == 0 {
			return nil
		}
		return MemoryRecalled{AfterMessage: after, NoteIDs: added}
	}
}

func applyRecall(messages []core.Message, staged map[string][]core.Message) ([]core.Message, map[string]bool) {
	if len(staged) == 0 {
		return messages, nil
	}
	var out []core.Message
	var inserted map[string]bool
	for i, msg := range messages {
		if batch := staged[msg.ID]; len(batch) > 0 && recallBoundary(messages, i) {
			if out == nil {
				out = append(make([]core.Message, 0, len(messages)+len(staged)), messages[:i]...)
				inserted = make(map[string]bool, len(staged))
			}
			inserted[msg.ID] = true
			out = append(out, msg)
			out = append(out, batch...)
		} else if out != nil {
			out = append(out, msg)
		}
	}
	if out == nil {
		out = messages
	}
	return out, inserted
}

func (r *fastRecall) lookup(ctx context.Context, job *recallJob, covered func(system.Memory) bool) {
	defer job.cancel()
	scope := r.recaller.Recording.Completion(session.PurposeRecall)
	notes, err := r.recaller.selectNotes(ctx, r.store, job.query, scope, covered)
	if closeErr := scope.Close(); err != nil || closeErr != nil || ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() == nil && (r.current == job || r.previous == job) {
		job.notes = notes
	}
}
