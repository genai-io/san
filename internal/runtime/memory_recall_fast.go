package runtime

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// fastRecall owns one runtime's background lookups and their request-only
// messages. It never adds recalled notes to conversation history.
type fastRecall struct {
	ctx      context.Context
	store    system.MemoryStore
	recaller Recaller

	mu sync.Mutex

	// Keep the last two lookups so a late result can still be used after the
	// next input arrives.
	queuedQueries                map[string]string // User message ID to query.
	latestLookup, previousLookup *recallLookup

	retainedNotes  []retainedRecallNote
	batchID        uint64
	requestInserts map[string][]core.Message // Keyed by insertion point; frozen for retries.
	lastUsedNotes  map[string]bool           // Notes used by the last responding request.
}

type recallLookup struct {
	userID  string
	query   string
	ctx     context.Context
	cancel  context.CancelFunc
	matches []system.Memory // Selected references, not cached note bodies.
}

type retainedRecallNote struct {
	note             system.Memory
	windowUserID     string // User message whose age bounds retention.
	insertAfterID    string // Request message after which the batch first appeared.
	batchID          uint64
	fromEarlierInput bool
}

// Limit carryover by both recent user inputs (not tool steps) and note count.
const (
	recallUserWindow = 20
	maxRetainedNotes = 2 * maxRecalled
)

// MemoryRecalled reports newly used notes after the main request responds.
// It carries display metadata only, never note bodies or store paths.
type MemoryRecalled struct {
	AfterMessage string
	NoteIDs      []string
}

func (p BuildParams) newFastRecall(ctx context.Context) *fastRecall {
	mode := p.AutoMemory.Recall.Mode
	if mode == setting.MemoryRecallWait || mode == setting.MemoryRecallOff || p.AutoMemory.Store.Off() || p.Provider == nil {
		return nil
	}
	return &fastRecall{ctx: ctx, store: p.AutoMemory.Store, recaller: p.MemoryRecaller()}
}

func (r *fastRecall) requestContext() func([]core.Message, bool) ([]core.Message, func() core.Event) {
	if r == nil {
		return nil
	}
	return r.prepareRequest
}

func (r *fastRecall) queueQuery(userID, query string) {
	if r == nil || strings.TrimSpace(query) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queuedQueries == nil {
		r.queuedQueries = make(map[string]string)
	}
	r.queuedQueries[userID] = query
}

func (r *fastRecall) cancel() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.latestLookup != nil {
		r.latestLookup.cancel()
	}
	if r.previousLookup != nil {
		r.previousLookup.cancel()
	}
	r.latestLookup, r.previousLookup = nil, nil
	r.queuedQueries, r.retainedNotes, r.requestInserts = nil, nil, nil
}

func (r *fastRecall) prepareRequest(messages []core.Message, retry bool) ([]core.Message, func() core.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !retry {
		r.refreshRequest(messages)
	}
	prepared, insertedAfter := insertRecallMessages(messages, r.requestInserts)
	return prepared, r.recallNotice(insertedAfter)
}

func (r *fastRecall) refreshRequest(messages []core.Message) {
	lookup := r.takeLookup(messages)
	if lookup == nil && len(r.retainedNotes) == 0 && !r.latestLookup.hasMatches() && !r.previousLookup.hasMatches() {
		r.requestInserts = nil
		return
	}
	existing := recallBlocks(messages)
	covered := r.refreshInserts(messages, existing)
	if lookup != nil {
		go r.runLookup(lookup, func(note system.Memory) bool {
			return covered[memoryID(note)] || existing.covers(r.store, note)
		})
	}
}

// Take the newest queued query present in this request. A message already used
// for lookup stops the scan so older queries cannot start on a later request.
func (r *fastRecall) takeLookup(messages []core.Message) *recallLookup {
	for i := len(messages) - 1; i >= 0 && len(r.queuedQueries) > 0; i-- {
		userID := messages[i].ID
		if query, ok := r.queuedQueries[userID]; ok {
			if r.previousLookup != nil {
				r.previousLookup.cancel()
			}
			r.previousLookup = r.latestLookup
			for _, msg := range messages[:i+1] {
				delete(r.queuedQueries, msg.ID)
			}
			ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
			lookup := &recallLookup{userID: userID, query: query, ctx: ctx, cancel: cancel}
			r.latestLookup = lookup
			return lookup
		}
		if r.latestLookup != nil && userID == r.latestLookup.userID {
			break
		}
	}
	return nil
}

func (l *recallLookup) hasMatches() bool { return l != nil && len(l.matches) > 0 }

// Only ordinary user input counts toward the carryover window.
func isRecallUserInput(msg core.Message) bool {
	return msg.ID != "" && msg.Role == ai.RoleUser && !msg.Content.Has(ai.BlockToolCall) && !msg.Content.Has(ai.BlockToolResult) &&
		(msg.Content.Has(ai.BlockText) || msg.Content.Has(ai.BlockImage))
}

// A recall message cannot interrupt an assistant tool call or a run of results.
func canInsertRecallAfter(messages []core.Message, i int) bool {
	msg := messages[i]
	return msg.ID != "" && !msg.Content.Has(ai.BlockToolCall) &&
		(i+1 == len(messages) || !messages[i+1].Content.Has(ai.BlockToolResult))
}

// Refresh note bodies for a new request without moving batches that already
// appeared. New batches go after the native tail, never inside a prior message.
func (r *fastRecall) refreshInserts(messages []core.Message, existing activeRecallBlocks) map[string]bool {
	recentUsers := make(map[string]bool)
	safeAfter := make(map[string]bool, len(r.retainedNotes)+1)
	for _, note := range r.retainedNotes {
		safeAfter[note.insertAfterID] = false
	}
	var latestUserID string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if _, needed := safeAfter[msg.ID]; needed && canInsertRecallAfter(messages, i) {
			safeAfter[msg.ID] = true
		}
		if len(recentUsers) < recallUserWindow && isRecallUserInput(msg) {
			if latestUserID == "" {
				latestUserID = msg.ID
			}
			recentUsers[msg.ID] = true
		}
	}

	type renderedNote struct {
		retainedRecallNote
		body string
	}
	var rendered []renderedNote
	seen := make(map[string]bool)
	add := func(note retainedRecallNote) {
		id := memoryID(note.note)
		if !recentUsers[note.windowUserID] || !safeAfter[note.insertAfterID] || seen[id] {
			return
		}
		body := recalledNote(r.store, note.note)
		if body == "" || existing[id][recallBlock(body)] {
			return
		}
		seen[id] = true
		rendered = append(rendered, renderedNote{note, body})
	}
	for _, note := range r.retainedNotes {
		add(note)
	}
	if last := len(messages) - 1; latestUserID != "" && canInsertRecallAfter(messages, last) {
		afterID := messages[last].ID
		safeAfter[afterID] = true
		r.batchID++
		// Prefer the newer lookup if both selected the same note.
		for _, lookup := range []*recallLookup{r.latestLookup, r.previousLookup} {
			if lookup == nil || !recentUsers[lookup.userID] {
				continue
			}
			for _, note := range lookup.matches {
				add(retainedRecallNote{
					note: note, windowUserID: latestUserID, insertAfterID: afterID,
					batchID: r.batchID, fromEarlierInput: lookup.userID != latestUserID,
				})
			}
			lookup.matches = nil
		}
	}

	rendered = rendered[max(0, len(rendered)-maxRetainedNotes):]
	r.retainedNotes = r.retainedNotes[:0]
	r.requestInserts = make(map[string][]core.Message)
	covered := make(map[string]bool, len(rendered))
	for len(rendered) > 0 {
		first := rendered[0]
		var bodies, earlierBodies []string
		for len(rendered) > 0 && rendered[0].batchID == first.batchID {
			note := rendered[0]
			rendered = rendered[1:]
			r.retainedNotes = append(r.retainedNotes, note.retainedRecallNote)
			covered[memoryID(note.note)] = true
			if note.fromEarlierInput {
				earlierBodies = append(earlierBodies, note.body)
			} else {
				bodies = append(bodies, note.body)
			}
		}
		if len(earlierBodies) > 0 {
			bodies = append(bodies, "Recalled for the previous question; use only if relevant.")
			bodies = append(bodies, earlierBodies...)
		}
		msg := core.UserMessage(recallBlock(strings.Join(bodies, "\n\n")), nil)
		afterID := first.insertAfterID
		r.requestInserts[afterID] = append(r.requestInserts[afterID], msg)
	}
	return covered
}

func (r *fastRecall) recallNotice(insertedAfter map[string]bool) func() core.Event {
	if len(insertedAfter) == 0 && len(r.lastUsedNotes) == 0 {
		return nil
	}
	type usedNote struct{ id, afterID string }
	var used []usedNote
	usedIDs := make(map[string]bool, len(r.retainedNotes))
	for _, note := range r.retainedNotes {
		if insertedAfter[note.insertAfterID] {
			id := memoryID(note.note)
			usedIDs[id] = true
			used = append(used, usedNote{id, note.insertAfterID})
		}
	}
	return func() core.Event {
		r.mu.Lock()
		defer r.mu.Unlock()
		var added []string
		var afterID string
		for _, note := range used {
			if !r.lastUsedNotes[note.id] {
				added = append(added, note.id)
				afterID = note.afterID
			}
		}
		r.lastUsedNotes = usedIDs
		if len(added) == 0 {
			return nil
		}
		return MemoryRecalled{AfterMessage: afterID, NoteIDs: added}
	}
}

func insertRecallMessages(messages []core.Message, inserts map[string][]core.Message) ([]core.Message, map[string]bool) {
	if len(inserts) == 0 {
		return messages, nil
	}
	var out []core.Message
	var insertedAfter map[string]bool
	for i, msg := range messages {
		if recallMessages := inserts[msg.ID]; len(recallMessages) > 0 && canInsertRecallAfter(messages, i) {
			if out == nil {
				out = append(make([]core.Message, 0, len(messages)+len(inserts)), messages[:i]...)
				insertedAfter = make(map[string]bool, len(inserts))
			}
			insertedAfter[msg.ID] = true
			out = append(out, msg)
			out = append(out, recallMessages...)
		} else if out != nil {
			out = append(out, msg)
		}
	}
	if out == nil {
		out = messages
	}
	return out, insertedAfter
}

func (r *fastRecall) runLookup(lookup *recallLookup, covered func(system.Memory) bool) {
	defer lookup.cancel()
	matches, scope, err := r.recaller.selectNotes(lookup.ctx, r.store, lookup.query, covered)
	if closeErr := scope.Close(); err != nil || closeErr != nil || lookup.ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if lookup.ctx.Err() == nil && (r.latestLookup == lookup || r.previousLookup == lookup) {
		lookup.matches = matches
	}
}
