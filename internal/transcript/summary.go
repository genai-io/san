package transcript

import (
	"encoding/json"
	"os"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
)

type summaryCache struct {
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Item       Summary   `json:"summary"`
}

func (s *Store) summaryPath(id string) string { return s.transcriptPath(id) + ".meta" }

func (s *Store) summaryLocked(id string) (Summary, error) {
	info, err := os.Stat(s.transcriptPath(id))
	if err != nil {
		return Summary{}, err
	}
	if s.summaries == nil {
		s.summaries = make(map[string]summaryCache)
	}
	c, ok := s.summaries[id]
	if !ok {
		data, _ := os.ReadFile(s.summaryPath(id))
		_ = json.Unmarshal(data, &c)
	}
	if c.Item.ID == id && c.Size == info.Size() && c.ModifiedAt.Equal(info.ModTime()) {
		s.summaries[id] = c
		return c.Item, nil
	}
	item, err := s.buildSummaryLocked(id)
	if err != nil {
		return Summary{}, err
	}
	item.Title = preview(item.Title)
	item.LastPrompt = preview(item.LastPrompt)
	c = summaryCache{Size: info.Size(), ModifiedAt: info.ModTime(), Item: item}
	data, err := json.Marshal(c)
	if err != nil {
		return Summary{}, err
	}
	// A failed cache write never makes an intact transcript inaccessible.
	_ = atomicfile.Write(s.summaryPath(id), data, 0600)
	s.summaries[id] = c
	return item, nil
}

// Flush updates summaries for sessions touched by this store. Logs are already durable.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.sessions {
		if _, err := s.summaryLocked(id); err != nil {
			return err
		}
	}
	return nil
}

const previewRunes = 200

func preview(s string) string {
	if len(s) <= previewRunes {
		return s
	}
	runes := []rune(s)
	if len(runes) > previewRunes {
		return string(runes[:previewRunes])
	}
	return s
}
