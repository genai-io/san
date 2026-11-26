package memory

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/infra/atomicfile"
	"github.com/genai-io/san/internal/setting"
)

// RecallNote resolves a linked note without reading its body.
func RecallNote(store system.MemoryStore, id string) (system.Memory, bool) {
	if store.Off() || !validName(id) {
		return system.Memory{}, false
	}
	path := filepath.Join(string(store), id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return system.Memory{}, false
	}
	return system.Memory{Path: path}, true
}

const (
	maxNote  = 8 * 1024
	maxIndex = 128 * 1024
)

var errStale = errors.New("memory store changed before the update could be applied; retry the request")

// Serialize updates to the same canonical store without keeping
// locks forever for previously visited projects. Unrelated buckets can proceed.
var storeLocks [64]sync.Mutex

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
		return "", errors.New("invalid memory store directory")
	}
	// Resolve configured parent aliases (for example the platform temp/home
	// path), but never follow a symlink in place of the store itself.
	parent, err := atomicfile.ResolvePath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		return "", errors.New("memory store is not a real directory")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func openStore(path string) (*os.Root, fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, errors.New("memory store is not a real directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		_ = root.Close()
		return nil, nil, errStale
	}
	return root, info, nil
}

type revision struct {
	text string
	info fs.FileInfo
}

func (r revision) same(other revision) bool {
	if r.info == nil || other.info == nil {
		return r.info == nil && other.info == nil
	}
	return os.SameFile(r.info, other.info) && r.info.Mode() == other.info.Mode() &&
		r.info.ModTime() == other.info.ModTime() && r.text == other.text
}

func readRevision(root *os.Root, name string, limit int) (revision, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return revision{}, nil
	}
	if err != nil {
		return revision{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return revision{}, fmt.Errorf("not a bounded regular memory file: %s", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return revision{}, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return revision{}, errStale
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil {
		return revision{}, err
	}
	if len(data) > limit || !utf8.Valid(data) {
		return revision{}, fmt.Errorf("not a bounded UTF-8 memory file: %s", name)
	}
	return revision{string(data), info}, nil
}

type noteRevision struct {
	name string
	rev  revision
}

type storeSnapshot struct {
	dir        fs.FileInfo
	index      revision
	notes      []noteRevision
	noteBytes  int
	readRounds int
}

const (
	maxNotesPerRead = 2
	maxNoteText     = 16 * 1024
)

var noteName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}\.md$`)

func validName(name string) bool {
	if !noteName.MatchString(name) || strings.EqualFold(name, system.AutoMemoryIndexName) || strings.EqualFold(name, system.InstructionFile) {
		return false
	}
	// Reject reserved DOS device names on all platforms so notes can travel.
	base := strings.ToUpper(strings.TrimSuffix(name, ".md"))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	return !(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9')
}

func snapshotStore(ctx context.Context, path string) (s storeSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return s, err
	}
	root, dir, err := openStore(path)
	s.dir = dir
	if err != nil || root == nil {
		return s, err
	}
	defer func() { _ = root.Close() }()
	s.index, err = readRevision(root, system.AutoMemoryIndexName, maxIndex)
	if err != nil {
		return s, err
	}
	return s, ctx.Err()
}

func (s *storeSnapshot) readNotes(ctx context.Context, path string, files []string, visible map[string]bool) ([]noteData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > maxNotesPerRead || s.readRounds >= maxReadRounds {
		return nil, fmt.Errorf("memory read exceeds note count or round limits: %w", errInvalidProposal)
	}
	for i, file := range files {
		if !visible[file] || s.note(file).info != nil || slices.Contains(files[:i], file) {
			return nil, fmt.Errorf("memory read must request distinct, unread index IDs: %w", errInvalidProposal)
		}
	}
	current, err := canonicalPath(path)
	if err != nil || current != path {
		return nil, errStale
	}
	root, dir, err := openStore(path)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errStale
	}
	defer func() { _ = root.Close() }()
	if s.dir == nil || !os.SameFile(s.dir, dir) {
		return nil, errStale
	}
	checkIndex := func() error {
		index, err := readRevision(root, system.AutoMemoryIndexName, maxIndex)
		if err != nil || !s.index.same(index) {
			return errStale
		}
		return nil
	}
	if err := checkIndex(); err != nil {
		return nil, err
	}
	var revisions []noteRevision
	var evidence []noteData
	noteBytes := s.noteBytes
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rev, err := readRevision(root, file, maxNote)
		if err != nil {
			return nil, err
		}
		if rev.info == nil {
			return nil, errStale
		}
		noteBytes += len(rev.text)
		if noteBytes > maxNoteText {
			return nil, errors.New("memory note evidence exceeds text size limit")
		}
		revisions = append(revisions, noteRevision{file, rev})
		evidence = append(evidence, noteData{File: file, Text: rev.text})
	}
	if err := checkIndex(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || !os.SameFile(s.dir, info) {
		return nil, errStale
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.notes = append(s.notes, revisions...)
	s.noteBytes = noteBytes
	s.readRounds++
	return evidence, nil
}

func (s storeSnapshot) note(name string) revision {
	for _, note := range s.notes {
		if note.name == name {
			return note.rev
		}
	}
	return revision{}
}

func (s storeSnapshot) notesMatch(root *os.Root, committed map[string]bool) bool {
	for _, note := range s.notes {
		if committed[note.name] {
			continue
		}
		rev, err := readRevision(root, note.name, maxNote)
		if err != nil || !rev.same(note.rev) {
			return false
		}
	}
	return true
}

func apply(ctx context.Context, path string, s storeSnapshot, proposals ...proposal) (string, error) {
	h := fnv.New32a()
	_, _ = io.WriteString(h, strings.ToLower(path))
	lock := &storeLocks[h.Sum32()%uint32(len(storeLocks))]
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	current, err := canonicalPath(path)
	if err != nil || current != path {
		return "", errStale
	}
	root, dir, err := openStore(path)
	if err != nil {
		return "", err
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	if (s.dir == nil) != (dir == nil) || (dir != nil && !os.SameFile(s.dir, dir)) {
		return "", errStale
	}
	if root != nil {
		index, err := readRevision(root, system.AutoMemoryIndexName, maxIndex)
		if err != nil || !index.same(s.index) {
			return "", errStale
		}
		if !s.notesMatch(root, nil) {
			return "", errStale
		}
	}
	plan, err := planChanges(root, s, proposals)
	if err != nil || len(plan) == 0 {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if root == nil {
		root, err = createStore(path)
		if err != nil {
			return "", err
		}
		defer func() { _ = root.Close() }()
	}

	// Commit each step independently; a later failure does not undo prior steps.
	index := s.index
	committed := make(map[string]bool)
	var completed []string
	for _, step := range plan {
		var partial string
		err := ctx.Err()
		if err == nil {
			live, readErr := readRevision(root, system.AutoMemoryIndexName, maxIndex)
			// Recheck unchanged evidence before each commit. This cannot fence
			// an external writer between the check and the write.
			if readErr != nil || !live.same(index) || !s.notesMatch(root, committed) {
				err = errStale
			} else {
				partial, err = step.commit(root, &index)
			}
		}
		if err != nil {
			if len(completed) != 0 {
				err = fmt.Errorf("memory batch partially applied (%s); stopped at %s: %w", strings.Join(completed, ", "), step.File, err)
			}
			if partial != "" {
				completed = append(completed, partial)
			}
			if len(completed) != 0 {
				return "Memory · " + strings.Join(completed, ", ") + " (update incomplete)", err
			}
			return "", err
		}
		completed = append(completed, step.verb+" "+step.File)
		committed[step.File] = true
	}
	return "Memory · " + strings.Join(completed, ", "), nil
}

type plannedChange struct {
	proposal
	old   revision
	index string
	verb  string
}

func planChanges(root *os.Root, s storeSnapshot, proposals []proposal) ([]plannedChange, error) {
	if len(proposals) == 0 {
		return nil, errInvalidProposal
	}
	seen := make(map[string]bool, len(proposals))
	index := s.index.text
	var plan []plannedChange
	// Apply direct index edits before any automatic index maintenance, even
	// when the index edit appeared later in the proposal batch.
	for _, p := range proposals {
		if p.File != system.AutoMemoryIndexName {
			continue
		}
		key := strings.ToLower(p.File)
		if seen[key] || p.Action != actionUpdate || s.index.info == nil {
			return nil, errInvalidProposal
		}
		seen[key] = true
		if err := validateIndexEdit(index, p.Text); err != nil {
			return nil, err
		}
		if p.Text != index {
			index = p.Text
			plan = append(plan, plannedChange{proposal: p, index: index, verb: "updated"})
		}
	}
	for _, p := range proposals {
		if p.File == system.AutoMemoryIndexName {
			continue
		}
		key := strings.ToLower(p.File)
		if !validName(p.File) || seen[key] {
			return nil, errInvalidProposal
		}
		seen[key] = true
		old := s.note(p.File)
		if p.Action != actionForget && (len(p.Text) > maxNote || !utf8.ValidString(p.Text)) {
			return nil, errors.New("not a bounded UTF-8 memory note")
		}
		var verb string
		updateIndex := true
		switch p.Action {
		case actionRemember:
			if root != nil {
				if _, err := root.Lstat(p.File); err == nil {
					return nil, fmt.Errorf("memory note %s: %w", p.File, fs.ErrExist)
				} else if !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
			}
			verb = "saved"
		case actionUpdate:
			if old.info == nil {
				return nil, errInvalidProposal
			}
			meta, err := revisedMetadata(old.text, p.Text)
			if err != nil {
				return nil, err
			}
			if p.Text == old.text {
				continue
			}
			updateIndex = meta != nil
			if meta != nil {
				p.Name, p.Description, p.Type = meta.Name, meta.Description, meta.Type
			}
			verb = "updated"
		case actionForget:
			if len(proposals) != 1 || old.info == nil {
				return nil, errInvalidProposal
			}
			verb = "forgot"
		default:
			return nil, errInvalidProposal
		}
		if updateIndex {
			var err error
			index, err = editIndex(index, p)
			if err != nil {
				return nil, err
			}
		}
		plan = append(plan, plannedChange{p, old, index, verb})
	}
	return plan, nil
}

func validateIndexEdit(old, next string) error {
	if len(next) > maxIndex || !utf8.ValidString(next) {
		return errors.New("not a bounded UTF-8 memory index")
	}
	limits := setting.MemoryPreviewLimits{}
	before, problem := newIndex(old, limits).updateNoteIDs()
	if problem != "" {
		return errors.New(problem)
	}
	after, problem := newIndex(next, limits).updateNoteIDs()
	if problem != "" {
		return errors.New(problem)
	}
	if len(before) != len(after) {
		return errors.New("memory index edit must preserve note IDs")
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			return errors.New("memory index edit must preserve note IDs")
		}
	}
	return nil
}

// commit returns a description only if a failed step may have left a change.
func (p plannedChange) commit(root *os.Root, index *revision) (string, error) {
	if p.File == system.AutoMemoryIndexName {
		next, err := writeRevision(root, system.AutoMemoryIndexName, p.index, *index)
		if err == nil {
			*index = next
		}
		return "", err
	}
	if p.Action == actionRemember {
		if err := writeNote(root, p.File, p.Text, revision{}); err != nil {
			return "", err
		}
		next, err := writeRevision(root, system.AutoMemoryIndexName, p.index, *index)
		if err != nil {
			// Leave the complete note intact rather than risk deleting a note
			// an external writer may already have seen and edited.
			return "saved " + p.File + " without indexing it", fmt.Errorf("memory note created but index update failed (%s): %w", p.File, err)
		}
		*index = next
		return "", nil
	}
	return withIndexUpdate(root, index, p.index, func() error {
		if p.Action == actionUpdate {
			return writeNote(root, p.File, p.Text, p.old)
		}
		rev, err := readRevision(root, p.File, maxNote)
		if err != nil || !rev.same(p.old) {
			return errStale
		}
		return root.Remove(p.File)
	})
}

func withIndexUpdate(root *os.Root, current *revision, next string, change func() error) (string, error) {
	if next == current.text {
		return "", change()
	}
	written, err := writeRevision(root, system.AutoMemoryIndexName, next, *current)
	if err != nil {
		return "", err
	}
	if err := change(); err != nil {
		// Do not restore over changes made by another writer.
		if rollbackErr := writeNote(root, system.AutoMemoryIndexName, current.text, written); rollbackErr != nil {
			return "could not restore " + system.AutoMemoryIndexName, errors.Join(err, fmt.Errorf("memory index updated but could not be restored: %w", rollbackErr))
		}
		return "", err
	}
	*current = written
	return "", nil
}

func createStore(path string) (*os.Root, error) {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		root, _, err := openStore(parent)
		if err != nil {
			return nil, err
		}
		if root != nil {
			defer func() { _ = root.Close() }()
			rel, err := filepath.Rel(parent, path)
			if err != nil {
				return nil, err
			}
			if err := root.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
				return nil, err
			}
			// Unlike MkdirAll on the full path, this rejects a leaf that
			// appeared since the snapshot instead of using it.
			if err := root.Mkdir(rel, 0o700); err != nil {
				return nil, err
			}
			return root.OpenRoot(rel)
		}
		if filepath.Dir(parent) == parent {
			return nil, fs.ErrNotExist
		}
	}
}

// Use the same temp-and-rename strategy as atomicfile, but operate through the
// pinned root so a path swap cannot redirect a write outside the store. New
// files use an exclusive link instead of an overwriting rename.
func writeNote(root *os.Root, name, text string, old revision) error {
	_, err := writeRevision(root, name, text, old)
	return err
}

func writeRevision(root *os.Root, name, text string, old revision) (revision, error) {
	tmp := ".memory-update-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return revision{}, err
	}
	defer func() { _ = root.Remove(tmp) }()
	_, err = io.WriteString(f, text)
	if err == nil && old.info != nil && runtime.GOOS != "windows" {
		err = f.Chmod(old.info.Mode().Perm())
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return revision{}, err
	}
	// Capture the installed revision without adopting an external replacement.
	var info fs.FileInfo
	if name == system.AutoMemoryIndexName {
		info, err = root.Lstat(tmp)
		if err != nil {
			return revision{}, err
		}
	}
	if old.info == nil {
		err = root.Link(tmp, name)
	} else {
		limit := maxNote
		if name == system.AutoMemoryIndexName {
			limit = maxIndex
		}
		current, readErr := readRevision(root, name, limit)
		if readErr != nil || !current.same(old) {
			return revision{}, errStale
		}
		err = root.Rename(tmp, name)
	}
	if err != nil {
		return revision{}, err
	}
	return revision{text, info}, nil
}
