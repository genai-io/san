package session

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/genai-io/san/internal/transcript"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

const (
	defaultEventPageSize = 100
	maxEventPageSize     = 500
	maxEventCursorSize   = 2048
)

var ErrInvalidEventQuery = errors.New("invalid event query")

// EventQuery reads raw typed events, not rendered history or resumable context.
// Limit defaults to 100 and cannot exceed 500. Cursor is opaque to callers.
type EventQuery struct {
	Limit  int
	Cursor string
}

type EventPage struct {
	Events     []Event
	NextCursor string
	ThroughSeq uint64
}

type eventCursor struct {
	Version   int             `json:"version"`
	ProjectID string          `json:"project_id"`
	SessionID string          `json:"session_id"`
	Tail      sdksession.Tail `json:"tail"`
	Through   uint64          `json:"through"`
	After     uint64          `json:"after"`
}

// Events reads a checked prefix without taking a writer or repairing the file.
// Later pages keep that same prefix; appends do not change it. A changed or missing
// prefix fails instead of silently restarting. Legacy records are not converted.
// This does not make typed logs resumable through the existing runtime.
func (s *Store) Events(ctx context.Context, id string, query EventQuery) (EventPage, error) {
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	if err := transcript.ValidateStorageID(id); err != nil {
		return EventPage{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = defaultEventPageSize
	}
	if limit < 1 || limit > maxEventPageSize {
		return EventPage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidEventQuery, maxEventPageSize)
	}
	var cursor eventCursor
	var tailLimit *sdksession.Tail
	if query.Cursor != "" {
		if len(query.Cursor) > maxEventCursorSize {
			return EventPage{}, fmt.Errorf("%w: cursor is too large", ErrInvalidEventQuery)
		}
		data, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		if err != nil || strictEventJSON(data, &cursor) != nil {
			return EventPage{}, fmt.Errorf("%w: malformed cursor", ErrInvalidEventQuery)
		}
		hash, err := hex.DecodeString(cursor.Tail.Hash)
		if err != nil || len(hash) != 32 || cursor.Version != 1 || cursor.ProjectID != s.projectID || cursor.SessionID != id ||
			cursor.Tail.Sequence == 0 || cursor.Tail.Count == 0 || cursor.After == 0 || cursor.After >= cursor.Through {
			return EventPage{}, fmt.Errorf("%w: cursor does not match this query", ErrInvalidEventQuery)
		}
		tailLimit = &cursor.Tail
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	projection, tail, err := s.replayEvents(ctx, id, tailLimit)
	if err != nil {
		return EventPage{}, err
	}
	last := uint64(len(projection.data.Events))
	if tailLimit != nil && (cursor.Through != last || cursor.After >= last) {
		return EventPage{}, fmt.Errorf("%w: cursor does not match the checked prefix", ErrInvalidEventQuery)
	}
	start := int(cursor.After) // checked against the in-memory slice length above
	end := start + min(limit, len(projection.data.Events)-start)
	page := EventPage{Events: eventCopy(projection.data.Events[start:end]), ThroughSeq: last}
	if end < len(projection.data.Events) {
		data, err := json.Marshal(eventCursor{Version: 1, ProjectID: s.projectID, SessionID: id, Tail: tail, Through: last, After: uint64(end)})
		if err != nil {
			return EventPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}
