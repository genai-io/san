package core

import (
	"sort"
	"strings"
	"sync"
)

// system is the default System implementation.
//
// Sections are stored by Name and rendered by (Slot ascending, insertion order
// ascending) on Prompt(). Each section's rendered output is cached
// individually; the joined prompt is cached once per mutation cycle.
//
// Insertion order (not Name) breaks ties within a slot so callers control
// fine-grained order by registering sections in the order they want them
// rendered (e.g. user memory before project memory).
type system struct {
	mu       sync.RWMutex
	sections map[string]*sectionEntry
	counter  int // monotonic insertion sequence
	cached   string
	dirty    bool

	// observer is invoked synchronously on every Use/Drop after the mutation
	// applies. Set via SetObserver; nil until then. Reads happen on the same
	// goroutine that mutated, so no extra locking around the call.
	observer func(SystemChange)
}

type sectionEntry struct {
	def      Section
	inserted int // sequence number assigned on first Use
	cached   string
	fresh    bool
}

// NewSystem creates an empty System. For normal construction use the
// internal/core/system.Build helper, which Use's the stock sections.
func NewSystem() System {
	return &system{
		sections: make(map[string]*sectionEntry),
		dirty:    true,
	}
}

func (s *system) Prompt() string {
	s.mu.RLock()
	if !s.dirty {
		defer s.mu.RUnlock()
		return s.cached
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return s.cached
	}
	s.cached = s.build()
	s.dirty = false
	return s.cached
}

func (s *system) Use(sec Section, caller string) {
	s.mu.Lock()
	if e, ok := s.sections[sec.Name]; ok {
		// Replacing an existing section: preserve its insertion order so
		// position in the prompt does not jump around on hot updates.
		e.def = sec
		e.fresh = false
	} else {
		s.counter++
		s.sections[sec.Name] = &sectionEntry{def: sec, inserted: s.counter}
	}
	s.dirty = true
	obs := s.observer
	s.mu.Unlock()

	if obs != nil {
		obs(SystemChange{
			Name:    sec.Name,
			Slot:    int(sec.Slot),
			Content: renderSection(sec),
			Caller:  caller,
		})
	}
}

func (s *system) Drop(name, caller string) {
	s.mu.Lock()
	_, existed := s.sections[name]
	if existed {
		delete(s.sections, name)
		s.dirty = true
	}
	obs := s.observer
	s.mu.Unlock()

	if existed && obs != nil {
		obs(SystemChange{Name: name, Removed: true, Caller: caller})
	}
}

func (s *system) Refresh(name, caller string) {
	s.mu.Lock()
	var (
		obs     func(SystemChange)
		changed bool
		sec     Section
	)
	if e, ok := s.sections[name]; ok {
		e.fresh = false
		s.dirty = true
		changed = true
		sec = e.def
		obs = s.observer
	}
	s.mu.Unlock()

	if changed && obs != nil {
		obs(SystemChange{
			Name:    sec.Name,
			Slot:    int(sec.Slot),
			Content: renderSection(sec),
			Caller:  caller,
		})
	}
}

// Sections returns a snapshot of currently registered sections in render order.
func (s *system) Sections() []Section {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := s.sortedEntries()
	out := make([]Section, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.def)
	}
	return out
}

// SetObserver attaches an observer for section mutations. On attach, the
// existing sections are replayed as synthetic "added" events so the observer
// sees a complete history starting from this moment.
func (s *system) SetObserver(fn func(SystemChange)) {
	s.mu.Lock()
	s.observer = fn
	entries := s.sortedEntries()
	s.mu.Unlock()

	if fn == nil {
		return
	}
	for _, e := range entries {
		fn(SystemChange{
			Name:    e.def.Name,
			Slot:    int(e.def.Slot),
			Content: renderSection(e.def),
			Caller:  "system:init",
		})
	}
}

// renderSection invokes a Section's Render func once, returning "" on nil.
// Used by observers that need the rendered content without going through the
// cached-Prompt path (which joins all sections).
func renderSection(sec Section) string {
	if sec.Render == nil {
		return ""
	}
	return sec.Render()
}

// sortedEntries returns entries in render order (Slot, insertion order).
func (s *system) sortedEntries() []*sectionEntry {
	entries := make([]*sectionEntry, 0, len(s.sections))
	for _, e := range s.sections {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].def.Slot != entries[j].def.Slot {
			return entries[i].def.Slot < entries[j].def.Slot
		}
		return entries[i].inserted < entries[j].inserted
	})
	return entries
}

// build assembles all non-empty sections in render order, separated by blank
// lines. Section.Render returning "" is treated as "skip this section".
// Per-section results are cached so unchanged sections don't re-render.
func (s *system) build() string {
	entries := s.sortedEntries()
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.fresh {
			if e.def.Render != nil {
				e.cached = e.def.Render()
			} else {
				e.cached = ""
			}
			e.fresh = true
		}
		if e.cached != "" {
			parts = append(parts, e.cached)
		}
	}
	return strings.Join(parts, "\n\n")
}
