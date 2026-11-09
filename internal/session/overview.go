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

// Overview is one session's row in a list, projected from its events. It is
// derived data: never authoritative, always rebuildable, and cached only to
// avoid replaying every session just to draw a picker.
type Overview struct {
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

func overviewOf(sessionID string, p *Projection) Overview {
	id, state, cfg := p.Identity(), p.State(), p.Config()
	s := Overview{SessionID: sessionID, AgentID: id.AgentID, Workspace: id.Workspace, InitialCWD: id.InitialCWD,
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

// stamp identifies the committed content of an events file without
// reading it all: the length of its complete lines and a digest of the last
// one. Each batch line carries a hash chained to every earlier batch, so equal
// stamps mean equal committed history. An unfinished last line, which
// readers ignore and the next writer cuts off, is not part of it.
type stamp struct {
	Size     int64  `json:"size"`
	LastLine string `json:"last_line"`
}

func stampOf(path string) (stamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return stamp{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return stamp{}, err
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
			return stamp{}, err
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
		return stamp{}, nil // no complete line: nothing committed
	}
	sum := sha256.Sum256(tail)
	return stamp{Size: end, LastLine: hex.EncodeToString(sum[:])}, nil
}

type overviewCacheFile struct {
	Stamp    stamp    `json:"stamp"`
	Overview Overview `json:"overview"`
}

// overviewCache keeps one derived file per session. A cache entry is valid only
// while the events file still has the stamp it was built from, so it can
// lag behind a commit but never lead it; any doubt rebuilds.
type overviewCache struct{ dir string }

func (c overviewCache) path(id string) string { return filepath.Join(c.dir, id+".json") }

func (c overviewCache) load(id string, st stamp) (Overview, bool) {
	data, err := os.ReadFile(c.path(id))
	if err != nil {
		return Overview{}, false
	}
	var f overviewCacheFile
	if strictEventJSON(data, &f) != nil || f.Stamp != st || f.Overview.SessionID != id {
		return Overview{}, false
	}
	return f.Overview, true
}

func (c overviewCache) store(id string, st stamp, s Overview) error {
	data, err := json.Marshal(overviewCacheFile{st, s})
	if err != nil {
		return err
	}
	return atomicfile.Write(c.path(id), data, 0o600)
}

func (c overviewCache) remove(id string) error {
	if err := os.Remove(c.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// overview returns the cached overview of a session when the events file is
// unchanged, otherwise replays it and refreshes the cache. A failed cache write
// never fails the read: the cache is an optimisation, not a record.
func (s *Store) overview(ctx context.Context, id string) (Overview, error) {
	cache := overviewCache{dir: filepath.Join(s.projectDir, "cache")}
	st, err := stampOf(s.TranscriptPath(id))
	if err != nil {
		return Overview{}, err
	}
	if st != (stamp{}) {
		if cached, ok := cache.load(id, st); ok {
			return cached, nil
		}
	}
	p, _, err := s.replayEvents(ctx, id, nil)
	if err != nil {
		return Overview{}, err
	}
	ov := overviewOf(id, p)
	// Cache only if the file did not change since the first stamp: then
	// the replay saw exactly the committed history the stamp names.
	if after, err := stampOf(s.TranscriptPath(id)); err == nil && after == st {
		_ = cache.store(id, st, ov)
	}
	return ov, nil
}
