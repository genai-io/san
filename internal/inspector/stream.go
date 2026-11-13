package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

// rawBatch retains the SDK record unchanged. Events are extracted only as raw
// JSON for presentation; domain validation belongs exclusively to session.Read.
type rawBatch struct {
	Tail  sdksession.Tail  `json:"tail"`
	Batch sdksession.Batch `json:"batch"`
}
type rawRecords struct {
	Batches     []rawBatch `json:"batches"`
	Error       string     `json:"error,omitempty"`
	Unvalidated string     `json:"unvalidatedText,omitempty"`
}

func (s *Server) readBatches(ctx context.Context, id string) ([]rawBatch, error) {
	batches := []rawBatch{}
	for b, err := range s.store.CommittedBatches(ctx, id) {
		if err != nil {
			return batches, err
		}
		batches = append(batches, rawBatch{Tail: b.Receipt.Tail, Batch: b.Batch})
	}
	return batches, nil
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, id string) {
	batches, err := s.readBatches(r.Context(), id)
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Retain the checked tail chain, not byte offsets or event IDs. Replacement
	// closes the stream rather than splicing unrelated histories together.
	var prior []sdksession.Tail
	emit := func(batches []rawBatch, readErr error) bool {
		if len(batches) < len(prior) {
			readErr = fmt.Errorf("committed prefix replaced")
		}
		for i, tail := range prior {
			if i >= len(batches) || batches[i].Tail != tail {
				readErr = fmt.Errorf("committed prefix replaced")
				break
			}
		}
		if readErr != nil {
			data, _ := json.Marshal(map[string]string{"error": readErr.Error()})
			fmt.Fprintf(w, "event: diagnostic\ndata: %s\n\n", data)
			flusher.Flush()
			return false
		}
		for _, b := range batches[len(prior):] {
			data, e := json.Marshal(b)
			if e != nil {
				return false
			}
			if _, e = fmt.Fprintf(w, "data: %s\n\n", data); e != nil {
				return false
			}
			prior = append(prior, b.Tail)
		}
		flusher.Flush()
		return true
	}
	// A corrupt suffix must not hide a valid committed prefix.
	if err != nil {
		if !emit(batches, nil) {
			return
		}
		emit(batches, err)
		return
	}
	if !emit(batches, nil) {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			batches, err = s.readBatches(r.Context(), id)
			if err != nil { // publish any newly checked batches before the diagnostic
				if len(batches) >= len(prior) {
					if !emit(batches, nil) {
						return
					}
				}
				emit(batches, err)
				return
			}
			if !emit(batches, nil) {
				return
			}
		}
	}
}
