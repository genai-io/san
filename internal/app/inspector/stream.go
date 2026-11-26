package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

type streamStamp struct{ info os.FileInfo }

func (s *streamStamp) changed(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		s.info = nil
		return true // let CommittedBatches diagnose the failure
	}
	prior := s.info
	// Capture the stamp before reading so a concurrent append is not marked read.
	s.info = info
	// On Windows SameFile may load the file ID lazily by path. Resolve it
	// now, not on the next poll when the name might refer to another file.
	if runtime.GOOS == "windows" && !os.SameFile(info, info) {
		s.info = nil
		return true
	}
	return prior == nil || prior.Size() != info.Size() || !prior.ModTime().Equal(info.ModTime()) ||
		!os.SameFile(prior, info) || !reflect.DeepEqual(changeTime(prior), changeTime(info))
}

// Unix change time catches in-place rewrites with restored mtime. Do not
// compare the whole stat record: reading the log can change its access time.
func changeTime(info os.FileInfo) any {
	stat := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if stat.Kind() == reflect.Struct {
		for _, name := range [...]string{"Ctim", "Ctimespec"} {
			if v := stat.FieldByName(name); v.IsValid() && v.CanInterface() {
				return v.Interface()
			}
		}
	}
	return nil
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	started := false
	start := func() bool {
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return false
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			started = true
		}
		return true
	}
	path := filepath.Join(s.store.ProjectDir(), "sessions", id, "events.jsonl")
	var stamp streamStamp
	// Retain only the checked tail chain. Do not emit anything new until
	// the entire previously sent prefix has been matched.
	var prior []sdksession.Tail
	poll := func(initial bool) bool {
		if !stamp.changed(path) {
			return true
		}
		var readErr error
		checked, prefixLen := 0, len(prior)
		for b, err := range s.store.CommittedBatches(r.Context(), id) {
			if err != nil {
				readErr = err
				break
			}
			tail := b.Receipt.Tail
			if checked < prefixLen {
				if prior[checked] != tail {
					break
				}
				checked++
				continue
			}
			if !start() {
				return false
			}
			data, err := json.Marshal(rawBatch{Tail: tail, Batch: b.Batch})
			if err != nil {
				return false
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return false
			}
			prior = append(prior, tail)
		}
		if checked < prefixLen {
			readErr = fmt.Errorf("committed prefix replaced")
		}
		if initial && !started && os.IsNotExist(readErr) {
			http.NotFound(w, r)
			return false
		}
		if !start() {
			return false
		}
		if readErr != nil {
			data, _ := json.Marshal(map[string]string{"error": readErr.Error()})
			fmt.Fprintf(w, "event: diagnostic\ndata: %s\n\n", data)
		}
		flusher.Flush()
		return readErr == nil
	}
	if !poll(true) {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !poll(false) {
				return
			}
		}
	}
}
