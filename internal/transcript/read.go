package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
)

// Read decodes current-format records, ignoring only an incomplete final fragment.
func Read(input io.Reader) iter.Seq2[Record, error] {
	return func(yield func(Record, error) bool) {
		reader := bufio.NewReader(input)
		first := true
		for {
			line, readErr := reader.ReadBytes('\n')
			if readErr != nil && readErr != io.EOF {
				yield(Record{}, readErr)
				return
			}
			if len(bytes.TrimSpace(line)) == 0 {
				if readErr == io.EOF {
					return
				}
				continue
			}
			var rec Record
			if err := json.Unmarshal(line, &rec); err != nil {
				if readErr == io.EOF {
					return
				}
				yield(Record{}, fmt.Errorf("decode transcript record: %w", err))
				return
			}
			if first && (rec.Type != SessionStarted || rec.Version != SchemaVersion) {
				yield(Record{}, fmt.Errorf("unsupported transcript version %q (want %s)", rec.Version, SchemaVersion))
				return
			}
			first = false
			if !yield(rec, nil) || readErr == io.EOF {
				return
			}
		}
	}
}
