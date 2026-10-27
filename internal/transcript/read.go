package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strconv"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/session"
)

// Records reads a session's records in order. A record's ID is its position
// in the log. A message is read as MessageAppended and a truncate as a
// ConversationReplaced carrying the messages it keeps.
func (s *Store) Records(ctx context.Context, id string) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		n := 0
		var conv []ai.Message
		for r, err := range s.log.Records(ctx, id) {
			var rec Record
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				rec, err = fromLog(r, &conv)
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

// fromLog reads one log record as a transcript record, keeping conv, the
// conversation so far, to say what a truncate leaves.
func fromLog(r session.Record, conv *[]ai.Message) (Record, error) {
	var err error
	if *conv, err = r.Apply(*conv); err != nil {
		return Record{}, err
	}
	switch r.Type {
	case session.TypeMessage:
		return Record{Type: MessageAppended, Time: r.Time, Message: r.Message}, nil
	case session.TypeTruncate:
		return Record{Type: ConversationReplaced, Time: r.Time, Messages: slices.Clone(*conv)}, nil
	}
	var rec Record
	if len(r.Data) > 0 {
		if err := json.Unmarshal(r.Data, &rec); err != nil {
			return Record{}, fmt.Errorf("decode transcript record %s: %w", r.Name, err)
		}
	}
	rec.Type = r.Name
	return rec, nil
}
