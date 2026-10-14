package inspector

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// handleStream serves SSE: client connects, we replay the file from the start,
// then poll for new content. Each emitted event carries one JSONL record line.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, sessionID string) {
	path, ok := s.sessionPath(sessionID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// First read happens before SSE headers go out so a missing file 404s
	// cleanly instead of sending an empty event stream.
	ctx := r.Context()
	var offset int64
	data, next, err := readFromOffset(path, offset)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if err := writeLines(w, flusher, data); err != nil {
		return
	}
	offset = next

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendNewLines(ctx, w, flusher, path, &offset); err != nil {
				return
			}
		case <-keepAlive.C:
			// Comment line keeps proxies / browsers from idle-timing out.
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}

// sendNewLines reads any new bytes past *offset and writes them as SSE
// messages. Advances *offset by the consumed byte count. Returns the first
// error from the writer (typically client disconnect).
func sendNewLines(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, path string, offset *int64) error {
	data, next, err := readFromOffset(path, *offset)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return ctx.Err()
	}
	if err := writeLines(w, flusher, data); err != nil {
		return err
	}
	*offset = next
	return nil
}

func writeLines(w http.ResponseWriter, flusher http.Flusher, data []byte) error {
	start := 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		line := data[start:i]
		start = i + 1
		if len(line) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
			return err
		}
	}
	flusher.Flush()
	return nil
}
