package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// EventSummary is one session's row in a list, projected from its events. It is
// derived data: never authoritative, always rebuildable, and cached only to
// avoid replaying every session just to draw a picker.
type EventSummary struct {
	SessionID  string `json:"session"`
	AgentID    string `json:"agent_id"`
	Workspace  string `json:"workspace"`
	InitialCWD string `json:"initial_cwd"`
	Parent     string `json:"parent,omitempty"`
	ForkedFrom string `json:"forked_from,omitempty"`
	Title      string `json:"title,omitempty"` // generated; a renamed title lives in Attrs
	LastPrompt string `json:"last_prompt,omitempty"`
	Model      string `json:"model,omitempty"`
	Messages   int    `json:"messages"`
	CreatedAt  int64  `json:"created_at"`
	// LastActive is the time of the last message.append. Catalogue, state and
	// annotation events, opening and reading do not count.
	LastActive int64 `json:"last_active"`
}

const lastPromptRunes = 200

func summarize(sessionID string, p *Projection) EventSummary {
	id, state, cfg := p.Identity(), p.State(), p.Config()
	s := EventSummary{SessionID: sessionID, AgentID: id.AgentID, Workspace: id.Workspace, InitialCWD: id.InitialCWD,
		Messages: len(p.data.Messages), CreatedAt: p.data.Events[0].Time}
	if id.Parent != nil {
		s.Parent = id.Parent.Session
	}
	if id.Fork != nil {
		s.ForkedFrom = id.Fork.Session
	}
	if state.Title != nil {
		s.Title = *state.Title
	}
	if cfg.Model != nil {
		s.Model = *cfg.Model
	}
	s.LastActive = s.CreatedAt
	for i := len(p.data.Events) - 1; i >= 0; i-- {
		if p.data.Events[i].Type == EventMessageAppend {
			s.LastActive = p.data.Events[i].Time
			break
		}
	}
	for i := len(p.data.Messages) - 1; i >= 0; i-- {
		if m := p.data.Messages[i]; m.Role == ai.RoleUser {
			if text := strings.TrimSpace(m.Text()); text != "" {
				s.LastPrompt = truncateRunes(text, lastPromptRunes)
				break
			}
		}
	}
	return s
}

func truncateRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// logFingerprint identifies the committed content of an events file without
// reading it all: the length of its complete lines and a digest of the last
// one. Each batch line carries a hash chained to every earlier batch, so equal
// fingerprints mean equal committed history. An unfinished last line, which
// readers ignore and the next writer cuts off, is not part of it.
type logFingerprint struct {
	Size     int64  `json:"size"`
	LastLine string `json:"last_line"`
}

func fingerprintLog(path string) (logFingerprint, error) {
	f, err := os.Open(path)
	if err != nil {
		return logFingerprint{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return logFingerprint{}, err
	}
	const chunk = 64 << 10
	var tail []byte // bytes from the current position to the end of the file
	end := int64(-1)
	for pos := info.Size(); pos > 0; {
		n := int64(chunk)
		if pos < n {
			n = pos
		}
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil && !errors.Is(err, io.EOF) {
			return logFingerprint{}, err
		}
		tail = append(buf, tail...)
		if end < 0 {
			// The complete part ends at the last newline.
			i := bytes.LastIndexByte(tail, '\n')
			if i < 0 {
				continue
			}
			end = pos + int64(i) + 1
			tail = tail[:i] // drop the unfinished line and the final newline
		}
		if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
			break
		}
	}
	if end < 0 {
		return logFingerprint{}, nil // no complete line: nothing committed
	}
	sum := sha256.Sum256(tail)
	return logFingerprint{Size: end, LastLine: hex.EncodeToString(sum[:])}, nil
}

type summaryCacheFile struct {
	Fingerprint logFingerprint `json:"fingerprint"`
	Summary     EventSummary   `json:"summary"`
}

// summaryCache keeps one derived file per session. A cache entry is valid only
// while the events file still has the fingerprint it was built from, so it can
// lag behind a commit but never lead it; any doubt rebuilds.
type summaryCache struct{ dir string }

func (c summaryCache) path(id string) string { return filepath.Join(c.dir, id+".json") }

func (c summaryCache) load(id string, fp logFingerprint) (EventSummary, bool) {
	data, err := os.ReadFile(c.path(id))
	if err != nil {
		return EventSummary{}, false
	}
	var f summaryCacheFile
	if strictEventJSON(data, &f) != nil || f.Fingerprint != fp || f.Summary.SessionID != id {
		return EventSummary{}, false
	}
	return f.Summary, true
}

func (c summaryCache) store(id string, fp logFingerprint, s EventSummary) error {
	data, err := json.Marshal(summaryCacheFile{fp, s})
	if err != nil {
		return err
	}
	return atomicfile.Write(c.path(id), data, 0o600)
}

func (c summaryCache) remove(id string) error {
	if err := os.Remove(c.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// eventSummary returns the cached summary of a session when the events file is
// unchanged, otherwise replays it and refreshes the cache. A failed cache write
// never fails the read: the cache is an optimisation, not a record.
func (s *Store) eventSummary(ctx context.Context, id string) (EventSummary, error) {
	cache := summaryCache{dir: filepath.Join(s.projectDir, "cache")}
	fp, err := fingerprintLog(s.TranscriptPath(id))
	if err != nil {
		return EventSummary{}, err
	}
	if fp != (logFingerprint{}) {
		if sum, ok := cache.load(id, fp); ok {
			return sum, nil
		}
	}
	p, _, err := s.replayEvents(ctx, id, nil)
	if err != nil {
		return EventSummary{}, err
	}
	sum := summarize(id, p)
	// Cache only if the file did not change since the first fingerprint: then
	// the replay saw exactly the committed history the fingerprint names.
	if after, err := fingerprintLog(s.TranscriptPath(id)); err == nil && after == fp {
		_ = cache.store(id, fp, sum)
	}
	return sum, nil
}
