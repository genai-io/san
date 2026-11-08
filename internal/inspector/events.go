package inspector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/transcript"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

// EventReader reads checked typed prefixes, never runtime history or writer handles.
type EventReader interface {
	Events(context.Context, string, session.EventQuery) (session.EventPage, error)
}

// NewWithEventReader adds typed inspection. The caller owns the reader's lifetime.
func NewWithEventReader(projectDir string, reader EventReader) *Server {
	s := New(projectDir)
	s.events = reader
	return s
}

// Close releases the legacy reader; injected event readers remain caller-owned.
func (s *Server) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

func eventError(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}{code, err.Error()})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := transcript.ValidateStorageID(id); err != nil {
		eventError(w, http.StatusBadRequest, "invalid_query", err)
		return
	}
	query := session.EventQuery{Cursor: r.URL.Query().Get("cursor")}
	if raw, ok := r.URL.Query()["limit"]; ok {
		limit, err := strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 500 {
			eventError(w, http.StatusBadRequest, "invalid_query", session.ErrInvalidEventQuery)
			return
		}
		query.Limit = limit
	}
	var page session.EventPage
	var err error
	if s.events == nil {
		// Keep New(projectDir)'s legacy viewer working without inventing a
		// typed reader with a guessed project identity. Probe through the
		// existing read-only format check, never through a writer.
		if s.store != nil {
			err = s.store.CheckLegacyFormat(r.Context(), id)
			if err == nil {
				err = sdksession.ErrMode
			}
		}
		if s.store == nil || errors.Is(err, transcript.ErrEventFormat) {
			eventError(w, http.StatusServiceUnavailable, "reader_unavailable", errors.New("typed event reader unavailable"))
			return
		}
	} else {
		page, err = s.events.Events(r.Context(), id, query)
	}
	if err != nil {
		status, code := http.StatusUnprocessableEntity, "invalid_log"
		switch {
		case errors.Is(err, sdksession.ErrMode):
			status, code = http.StatusConflict, "legacy_format"
		case errors.Is(err, session.ErrInvalidEventQuery):
			status, code = http.StatusBadRequest, "invalid_query"
		case errors.Is(err, os.ErrNotExist), errors.Is(err, sdksession.ErrNotFound):
			status, code = http.StatusNotFound, "not_found"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			status, code = http.StatusRequestTimeout, "cancelled"
		}
		eventError(w, status, code, err)
		return
	}
	// Keep native JSON numbers out of the browser's float64 parser. Timeline
	// fields are separate; payload detail is rendered as this opaque string.
	type eventView struct {
		ID      string               `json:"id"`
		Seq     uint64               `json:"seq"`
		Type    session.EventType    `json:"type"`
		Outcome session.EventOutcome `json:"outcome,omitempty"`
		JSON    string               `json:"json"`
	}
	views := make([]eventView, 0, len(page.Events))
	for _, event := range page.Events {
		data, err := json.MarshalIndent(event, "", "  ")
		if err != nil {
			eventError(w, http.StatusInternalServerError, "encoding_error", err)
			return
		}
		views = append(views, eventView{event.ID, event.Seq, event.Type, event.Outcome, string(data)})
	}
	writeJSON(w, struct {
		Events     []eventView `json:"events"`
		NextCursor string      `json:"nextCursor"`
		ThroughSeq uint64      `json:"throughSeq"`
	}{views, page.NextCursor, page.ThroughSeq})
}
