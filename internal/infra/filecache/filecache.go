package filecache

import (
	"sort"
	"sync"
	"time"
)

const (
	maxEntries        = 20
	restoreMaxFiles   = 5
	restoreMaxPerFile = 5000
	restoreMaxTotal   = 50000
)

type entry struct {
	FilePath  string
	Timestamp time.Time
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

func New() *Cache {
	return &Cache{entries: make(map[string]entry)}
}

func (c *Cache) Touch(filePath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[filePath] = entry{FilePath: filePath, Timestamp: time.Now()}
	c.evict()
}

// Clear forgets every touched file, for a new conversation.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

func (c *Cache) Recent(n int) []entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries := make([]entry, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	if n > 0 && len(entries) > n {
		entries = entries[:n]
	}
	return entries
}

func (c *Cache) evict() {
	if len(c.entries) <= maxEntries {
		return
	}
	entries := make([]entry, 0, len(c.entries))
	for _, e := range c.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
	for i := 0; i < len(entries)-maxEntries; i++ {
		delete(c.entries, entries[i].FilePath)
	}
}
