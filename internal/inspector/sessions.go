package inspector

import (
	"encoding/base64"
	"encoding/json"
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
	// Generated titles are optional sidebar metadata, not a reason to replay large logs.
	sessionTitleBudget = 512 << 10
)

type sessionListItem struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Size       int64     `json:"size"`
	AttrsError string    `json:"attrsError,omitempty"`
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
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]sessionListItem, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
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
		if after.ID == "" || compareSessions(item, sessionListItem{ID: after.ID, UpdatedAt: after.UpdatedAt}) > 0 {
			items = append(items, item)
		}
	}
	slices.SortFunc(items, compareSessions)
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
		item := &page.Items[i]
		if attrs, err := s.store.Attrs(ctx, item.ID); err != nil {
			item.AttrsError = err.Error()
		} else {
			item.Title = attrs.Title
		}
		if item.Title == "" && item.Size > 0 && item.Size <= budget {
			budget -= item.Size
			if projection, err := s.store.Read(ctx, item.ID); err == nil {
				if title := projection.State().Title; title != nil {
					item.Title = *title
				}
			}
		}
	}
	writeJSON(w, page)
}
