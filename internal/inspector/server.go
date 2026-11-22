// Package inspector serves a read-only local viewer of typed session events.
package inspector

import (
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/genai-io/san/internal/inspector/ui"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
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

// Handler serves only requests addressed to a loopback name. Binding to
// loopback is not enough: a page that rebinds its own domain to 127.0.0.1
// would otherwise read every transcript through the browser.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
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
	case len(parts) == 2 && parts[1] == "summary":
		s.handleSessionSummary(w, r, id)
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
	case len(parts) == 3 && parts[1] == "request":
		seq, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil || seq == 0 {
			http.NotFound(w, r)
			return
		}
		request, err := s.store.InferenceRequest(r.Context(), id, seq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("view") == "update-prefix" {
			ctx := r.Context()
			projection, err := s.store.Read(ctx, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			writeJSON(w, compareMaintainPrefix(projection.Events(), id, seq, request, func(source uint64) (*ai.Request, error) {
				return s.store.InferenceRequest(ctx, id, source)
			}))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(request)
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
