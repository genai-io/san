// Package inspector serves the san session transcripts to a local web
// UI. The inspector is read-only, single-process, localhost-only. It exposes
// a small HTTP API that mirrors the JSONL on disk — the wire format IS the
// file format — plus an SSE endpoint for live tail. UI assets are embedded
// so the binary is self-contained.
package inspector

import (
	"bytes"
	"container/list"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/inspector/ui"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// replayCacheCapacity is the upper bound on cached replay states across all
// sessions. Each entry is a fully materialized replayState (system sections,
// tool schemas, active message chain at that point in time). A few dozen
// entries cover sequential timeline navigation comfortably while keeping
// peak memory in the low single-digit MB.
const replayCacheCapacity = 64

// Server hosts the trace viewer for a single project directory.
type Server struct {
	projectDir string // .../.san/projects/<workdir-hash>
	mux        *http.ServeMux
	store      *transcript.Store // reads logs and derived summaries

	replayCache *replayLRU
}

func New(projectDir string) *Server {
	s := &Server{
		projectDir:  projectDir,
		mux:         http.NewServeMux(),
		replayCache: newReplayLRU(replayCacheCapacity),
	}
	// Best-effort: the viewer falls back to a directory walk if the store
	// isn't constructable (missing project dir, permission error, etc.).
	if store, err := transcript.NewStore(projectDir, ""); err == nil {
		s.store = store
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// UI assets — strip the "assets/" prefix to expose them at the root.
	assets, err := fs.Sub(ui.Assets, "assets")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(assets)))
	}

	s.mux.HandleFunc("/api/sessions", s.handleListSessions)
	// Per-session routes: /api/sessions/{id}/records, /api/sessions/{id}/stream,
	// /api/sessions/{id}/state/{recordID}
	s.mux.HandleFunc("/api/sessions/", s.handleSessionRoute)
}

// transcriptsDir is the JSONL location: <projectDir>/transcripts.
func (s *Server) transcriptsDir() string {
	return filepath.Join(s.projectDir, "transcripts")
}

type sessionListItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updatedAt"`
	Size      int64     `json:"size"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	var summaries []transcript.Summary
	if s.store != nil {
		summaries, _ = s.store.List(r.Context(), "", transcript.ListOptions{IncludeSidechain: true})
	}
	items, err := s.listFromDir(summaries)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// The inspector keeps raw file access even when a transcript cannot be restored.
func (s *Server) listFromDir(summaries []transcript.Summary) ([]sessionListItem, error) {
	byID := make(map[string]transcript.Summary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	entries, err := os.ReadDir(s.transcriptsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []sessionListItem{}, nil
		}
		return nil, err
	}
	items := make([]sessionListItem, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(s.transcriptsDir(), e.Name())
		item := sessionListItem{
			ID:        id,
			UpdatedAt: info.ModTime(),
			Size:      info.Size(),
		}
		if summary, ok := byID[id]; ok {
			item.Title, item.UpdatedAt = summary.Title, summary.UpdatedAt
		} else {
			item.Title = previewTitle(path)
		}
		items = append(items, item)
	}
	slices.SortStableFunc(items, func(a, b sessionListItem) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return items, nil
}

// handleSessionRoute routes /api/sessions/{id}/{action} to the matching handler.
func (s *Server) handleSessionRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	sessionID, action := parts[0], parts[1]
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case action == "records":
		s.handleRecords(w, r, sessionID)
	case action == "stream":
		s.handleStream(w, r, sessionID)
	case strings.HasPrefix(action, "state/"):
		recordID := strings.TrimPrefix(action, "state/")
		s.handleState(w, r, sessionID, recordID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request, sessionID, recordID string) {
	path, ok := s.sessionPath(sessionID)
	if !ok || recordID == "" || strings.ContainsAny(recordID, "/\\") {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", sessionID, recordID, info.Size(), info.ModTime().UnixNano())
	if cached, ok := s.replayCache.get(key); ok {
		writeJSON(w, cached)
		return
	}
	state, found, err := replayFile(path, recordID)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.replayCache.put(key, state)
	writeJSON(w, state)
}

// replayLRU caches the results of replayFile by (sessionID, recordID).
// The key includes file size and modification time so replacement invalidates it. Bounded to keep
// peak memory in check on long-running viewers; eviction is plain LRU.
type replayLRU struct {
	mu       sync.Mutex
	capacity int
	order    *list.List // front = most recently used
	entries  map[string]*list.Element
}

type replayCacheEntry struct {
	key   string
	state replayState
}

func newReplayLRU(capacity int) *replayLRU {
	return &replayLRU{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[string]*list.Element),
	}
}

func (c *replayLRU) get(key string) (replayState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*replayCacheEntry).state, true
	}
	return replayState{}, false
}

func (c *replayLRU) put(key string, state replayState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		el.Value.(*replayCacheEntry).state = state
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&replayCacheEntry{key: key, state: state})
	c.entries[key] = el
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*replayCacheEntry).key)
	}
}

func (s *Server) sessionPath(id string) (string, bool) {
	// Defense against path traversal — sessionID must look like a single
	// filename segment.
	if strings.ContainsAny(id, "/\\") || id == "" {
		return "", false
	}
	p := filepath.Join(s.transcriptsDir(), id+".jsonl")
	return p, true
}

func (s *Server) handleRecords(w http.ResponseWriter, r *http.Request, sessionID string) {
	path, ok := s.sessionPath(sessionID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	data, nextOffset, err := readFromOffset(path, after)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Next-Offset", strconv.FormatInt(nextOffset, 10))
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// readFromOffset returns complete JSONL lines and the consumed file offset.
// A complete EOF record gets a synthetic newline; partial JSON waits for more bytes.
func readFromOffset(path string, offset int64) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	end := info.Size()
	if offset >= end {
		return nil, end, nil
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, end-offset)
	n, err := f.Read(buf)
	if err != nil {
		return nil, 0, err
	}
	buf = buf[:n]
	i := bytes.LastIndexByte(buf, '\n')
	// A crash can leave a complete final record without the encoder's newline.
	if json.Valid(buf[i+1:]) {
		return append(buf, '\n'), offset + int64(n), nil
	}
	// Trim trailing partial line.
	if i >= 0 {
		return buf[:i+1], offset + int64(i+1), nil
	}
	// No newline yet — leave the bytes for next poll.
	return nil, offset, nil
}

// previewTitle returns a short prefix of the first user-role message's text
// content, suitable for a list view. Best-effort; returns "" on any error.
func previewTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	for rec, err := range transcript.Read(f) {
		if err != nil {
			return ""
		}
		messages := rec.Messages
		if rec.Message != nil {
			messages = []ai.Message{*rec.Message}
		}
		for _, msg := range messages {
			if msg.Role != ai.RoleUser || len(msg.ToolResults()) > 0 {
				continue
			}
			if text := strings.TrimSpace(msg.Text()); text != "" {
				runes := []rune(text)
				if len(runes) > 80 {
					return string(runes[:80]) + "…"
				}
				return text
			}
		}
	}
	return ""
}
