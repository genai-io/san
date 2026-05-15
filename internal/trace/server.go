// Package trace serves the gen-code session transcripts to a local web UI.
//
// The viewer is read-only, single-process, localhost-only. It exposes a small
// HTTP API that mirrors the JSONL on disk — the wire format IS the file
// format — plus an SSE endpoint for live tail. UI assets are embedded so the
// binary is self-contained.
package trace

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/gen-code/internal/trace/ui"
)

// Server hosts the trace viewer for a single project directory.
type Server struct {
	projectDir string // .../.gen/projects/<encoded-cwd>
	mux        *http.ServeMux

	mu       sync.Mutex
	watchers map[string]*tailer // sessionID -> active tailer
}

func New(projectDir string) *Server {
	s := &Server{
		projectDir: projectDir,
		mux:        http.NewServeMux(),
		watchers:   make(map[string]*tailer),
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
	// Per-session routes: /api/sessions/{id}/records, /api/sessions/{id}/stream
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
	entries, err := os.ReadDir(s.transcriptsDir())
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, []sessionListItem{})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
		item := sessionListItem{ID: id, UpdatedAt: info.ModTime(), Size: info.Size()}
		// First user-typed text in the file makes a decent title preview without
		// loading the whole transcript.
		item.Title = previewTitle(filepath.Join(s.transcriptsDir(), e.Name()))
		items = append(items, item)
	}
	// Newest first.
	sortByUpdatedDesc(items)
	writeJSON(w, items)
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

	switch action {
	case "records":
		s.handleRecords(w, r, sessionID)
	case "stream":
		s.handleStream(w, r, sessionID)
	default:
		http.NotFound(w, r)
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

// readFromOffset reads bytes from the file starting at offset. Returns the
// bytes plus the new end-of-file offset. Always returns at line boundaries —
// trailing partial line is left for the next read.
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
	// Trim trailing partial line.
	if i := lastNewline(buf); i >= 0 {
		return buf[:i+1], offset + int64(i+1), nil
	}
	// No newline yet — leave the bytes for next poll.
	return nil, offset, nil
}

func lastNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

// previewTitle returns a short prefix of the first user-role message's text
// content, suitable for a list view. Best-effort; returns "" on any error.
func previewTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for dec.More() {
		var rec struct {
			Type    string `json:"type"`
			Message *struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := dec.Decode(&rec); err != nil {
			return ""
		}
		if rec.Type != "message.appended" || rec.Message == nil || rec.Message.Role != "user" {
			continue
		}
		for _, c := range rec.Message.Content {
			if c.Type == "text" {
				t := strings.TrimSpace(c.Text)
				if t == "" {
					continue
				}
				if len(t) > 80 {
					t = t[:80] + "…"
				}
				return t
			}
		}
	}
	return ""
}

func sortByUpdatedDesc(items []sessionListItem) {
	// Insertion sort — list size is small (tens of sessions per project).
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && items[j].UpdatedAt.After(items[j-1].UpdatedAt) {
			items[j-1], items[j] = items[j], items[j-1]
			j--
		}
	}
}

