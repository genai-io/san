package inspector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/genai-io/san/internal/session"
)

const (
	sessionPageSize    = 10
	maxSessionPageSize = 50
	// Prompt previews are optional sidebar metadata, not a reason to replay large logs.
	sessionTitleBudget = 512 << 10
)

type sessionListItem struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Size       int64     `json:"size"`
	AttrsError string    `json:"attrsError,omitempty"`
	Subagent   bool      `json:"subagent,omitempty"`
	Parent     string    `json:"parent,omitempty"`
}

type sessionPage struct {
	Items []sessionListItem `json:"items"`
	Next  string            `json:"next,omitempty"`
}

type sessionCursor struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func compareSessions(a, b sessionListItem) int {
	if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
		return order
	}
	return strings.Compare(a.ID, b.ID)
}

func readSessionPage(ctx context.Context, root string, cursor sessionListItem, keep int) ([]sessionListItem, error) {
	items := make([]sessionListItem, 0, keep+1)
	dir, err := os.Open(root)
	if os.IsNotExist(err) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for ctx.Err() == nil {
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			return nil, err
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			id := entry.Name()
			if !entry.IsDir() || strings.HasPrefix(id, ".") || session.ValidateStorageID(id) != nil {
				continue
			}
			info, err := os.Stat(filepath.Join(root, id, "events.jsonl"))
			if err != nil {
				continue
			}
			item := sessionListItem{ID: id, UpdatedAt: info.ModTime(), Size: info.Size()}
			if cursor.ID != "" && compareSessions(item, cursor) <= 0 {
				continue
			}
			// Keep only the best page plus its lookahead in cursor order.
			i, _ := slices.BinarySearchFunc(items, item, compareSessions)
			if i < keep {
				items = slices.Insert(items, i, item)
				items = items[:min(len(items), keep)]
			}
		}
		if err == io.EOF {
			return items, nil
		}
	}
	return nil, ctx.Err()
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	limit := sessionPageSize
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxSessionPageSize {
			http.Error(w, "invalid session page size", http.StatusBadRequest)
			return
		}
		limit = n
	}
	var after sessionCursor
	if raw := query.Get("cursor"); raw != "" {
		if len(raw) > 512 {
			http.Error(w, "invalid session cursor", http.StatusBadRequest)
			return
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(data, &after) != nil || session.ValidateStorageID(after.ID) != nil || after.UpdatedAt.IsZero() {
			http.Error(w, "invalid session cursor", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	root := filepath.Join(s.store.ProjectDir(), "sessions")
	cursor := sessionListItem{ID: after.ID, UpdatedAt: after.UpdatedAt}
	items, err := readSessionPage(ctx, root, cursor, limit+1)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page := sessionPage{Items: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := page.Items[limit-1]
		data, _ := json.Marshal(sessionCursor{ID: last.ID, UpdatedAt: last.UpdatedAt})
		page.Next = base64.RawURLEncoding.EncodeToString(data)
	}
	budget := int64(sessionTitleBudget)
	for i := range page.Items {
		if ctx.Err() != nil {
			return
		}
		s.describeSession(ctx, &page.Items[i], &budget)
	}
	writeJSON(w, page)
}

func (s *Server) describeSession(ctx context.Context, item *sessionListItem, budget *int64) {
	if attrs, err := s.store.Attrs(ctx, item.ID); err != nil {
		item.AttrsError = err.Error()
	} else {
		item.Title = strings.TrimSpace(attrs.Title)
	}
	if item.Size <= 0 || item.Size > *budget {
		return
	}
	*budget -= item.Size
	projection, err := s.store.Read(ctx, item.ID)
	if err != nil {
		return
	}
	identity := projection.Identity()
	if identity.Parent != nil {
		item.Parent = identity.Parent.Session
	}
	item.Subagent = item.Parent != "" || (identity.AgentID != "" && identity.AgentID != "main")
	if item.Title == "" {
		if item.Subagent {
			item.Title = projection.FirstUserPrompt()
		} else {
			item.Title = projection.LastUserPrompt()
		}
	}
}

func (s *Server) handleSessionSummary(w http.ResponseWriter, r *http.Request, id string) {
	if session.ValidateStorageID(id) != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(filepath.Join(s.store.ProjectDir(), "sessions", id, "events.jsonl"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item := sessionListItem{ID: id, UpdatedAt: info.ModTime(), Size: info.Size()}
	budget := int64(sessionTitleBudget)
	s.describeSession(r.Context(), &item, &budget)
	writeJSON(w, item)
}
