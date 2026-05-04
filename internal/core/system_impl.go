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

func (s *system) Use(sec Section) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
}

func (s *system) Drop(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sections[name]; ok {
		delete(s.sections, name)
		s.dirty = true
	}
}

func (s *system) Refresh(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.sections[name]; ok {
		e.fresh = false
		s.dirty = true
	}
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
