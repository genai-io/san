package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/genai-io/san/internal/atomicfile"
)

// Attrs is user intent about a session: a title the person chose, archive and
// pin. It is not derivable from events, so it is authoritative and lives beside
// them, changeable without the writer lock. A missing file means all defaults.
type Attrs struct {
	Title      string `json:"title,omitempty"`
	ArchivedAt int64  `json:"archived_at,omitempty"` // Unix ms; 0 = not archived
	Pinned     bool   `json:"pinned,omitempty"`
}

// AttrsPatch changes named attributes; absent fields stay, cleared ones reset.
type AttrsPatch struct {
	Title      PatchField[string] `json:"title,omitzero"`
	ArchivedAt PatchField[int64]  `json:"archived_at,omitzero"`
	Pinned     PatchField[bool]   `json:"pinned,omitzero"`
}

const attrsFile = "attrs.json"

// patchTo sets *dst to the patched value, or to its zero value when cleared.
func patchTo[T any](dst *T, f PatchField[T]) {
	switch {
	case !f.Present:
	case f.Value != nil:
		*dst = *f.Value
	default:
		var zero T
		*dst = zero
	}
}

func (a Attrs) apply(p AttrsPatch) Attrs {
	patchTo(&a.Title, p.Title)
	patchTo(&a.ArchivedAt, p.ArchivedAt)
	patchTo(&a.Pinned, p.Pinned)
	return a
}

func (a Attrs) valid() bool { return a.ArchivedAt >= 0 && len(a.Title) <= 1024 }

// readAttrs reads dir/attrs.json. Damaged content is an error: it holds user
// intent, so it is never silently reset.
func readAttrs(dir string) (Attrs, error) {
	var a Attrs
	data, err := os.ReadFile(filepath.Join(dir, attrsFile))
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = strictEventJSON(data, &a); err != nil {
		return Attrs{}, err
	}
	return a, nil
}

// updateAttrs applies patch under a short cross-process lock and returns the
// result. Defaults are stored as no file, so most sessions never have one.
func updateAttrs(dir string, patch AttrsPatch) (Attrs, error) {
	var out Attrs
	err := atomicfile.WithLock(filepath.Join(dir, ".attrs.lock"), func() error {
		cur, err := readAttrs(dir)
		if err != nil {
			return err
		}
		out = cur.apply(patch)
		if !out.valid() {
			return errors.New("invalid session attributes")
		}
		path := filepath.Join(dir, attrsFile)
		if out == (Attrs{}) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		}
		data, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return atomicfile.Write(path, data, 0o600)
	})
	return out, err
}
