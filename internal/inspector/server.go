// Package inspector serves a read-only local viewer of typed session events.
package inspector

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/genai-io/san/internal/inspector/ui"
	"github.com/genai-io/san/internal/session"
)

type Server struct {
	store *session.Store
	mux   *http.ServeMux
}

func New(store *session.Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	assets, err := fs.Sub(ui.Assets, "assets")
	if err == nil {
		s.mux.Handle("/", http.FileServer(http.FS(assets)))
	}
	s.mux.HandleFunc("/api/sessions", s.handleListSessions)
	s.mux.HandleFunc("/api/sessions/", s.handleSessionRoute)
	return s
}
func (s *Server) Handler() http.Handler { return s.mux }

type sessionListItem struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Size       int64     `json:"size"`
	AttrsError string    `json:"attrsError,omitempty"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// Directory enumeration keeps unsupported sessions discoverable. Unlike List,
	// this read never regenerates overview caches or changes files.
	entries, err := os.ReadDir(filepath.Join(s.store.ProjectDir(), "sessions"))
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), 500)
		return
	}
	items := []sessionListItem{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(s.store.ProjectDir(), "sessions", entry.Name(), "events.jsonl"))
		if err != nil {
			continue
		}
		item := sessionListItem{ID: entry.Name(), UpdatedAt: info.ModTime(), Size: info.Size()}
		if p, e := s.store.Read(r.Context(), entry.Name()); e == nil {
			if title := p.State().Title; title != nil {
				item.Title = *title
			}
		}
		if attrs, e := s.store.Attrs(r.Context(), entry.Name()); e != nil {
			item.AttrsError = e.Error()
		} else if attrs.Title != "" {
			item.Title = attrs.Title
		}
		items = append(items, item)
	}
	slices.SortStableFunc(items, func(a, b sessionListItem) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	writeJSON(w, items)
}

func (s *Server) handleSessionRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 2 && parts[1] == "records":
		batches, err := s.readBatches(r.Context(), id)
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		result := rawRecords{Batches: batches}
		if err != nil {
			result.Error = err.Error()
			// Diagnostics are explicitly unvalidated, bounded text, never streamed as events.
			if f, e := os.Open(filepath.Join(s.store.ProjectDir(), "sessions", id, "events.jsonl")); e == nil {
				raw, _ := io.ReadAll(io.LimitReader(f, 1<<20))
				f.Close()
				result.Unvalidated = string(raw)
			}
		}
		writeJSON(w, result)
	case len(parts) == 2 && parts[1] == "stream":
		s.handleStream(w, r, id)
	case len(parts) == 3 && parts[1] == "state":
		seq, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		st, err := s.readState(r.Context(), id, seq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, st)
	default:
		http.NotFound(w, r)
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
