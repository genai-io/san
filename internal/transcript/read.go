package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strconv"

	"github.com/genai-io/sdk-go/pkg/session"
)

// Records reads a session's records in order. A record's ID is its position
// in the log. A message is read as MessageAppended and a reset as an empty
// ConversationReplaced.
func (s *Store) Records(ctx context.Context, id string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		n := 0
		for r, err := range s.log.Records(ctx, id) {
			var rec Record
			if err == nil {
				rec, err = fromLog(r)
			}
			if err == nil && n == 0 && (rec.Type != SessionStarted || rec.Version != SchemaVersion) {
				err = fmt.Errorf("unsupported transcript version %q (want %s)", rec.Version, SchemaVersion)
			}
			rec.ID = strconv.Itoa(n)
			n++
			if !yield(rec, err) || err != nil {
				return
			}
		}
	}
}

func fromLog(r session.Record) (Record, error) {
	switch r.Type {
	case "message":
		return Record{Type: MessageAppended, Time: r.Time, Message: r.Message}, nil
	case "reset":
		return Record{Type: ConversationReplaced, Time: r.Time}, nil
	}
	var rec Record
	if len(r.Data) > 0 {
		if err := json.Unmarshal(r.Data, &rec); err != nil {
			return Record{}, fmt.Errorf("decode transcript record %s: %w", r.Type, err)
		}
	}
	rec.Type = r.Type
	return rec, nil
}
