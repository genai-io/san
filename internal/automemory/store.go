package automemory

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
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/core/system"
)

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

func (s *storeSnapshot) readNotes(ctx context.Context, path string, files []string) ([]noteData, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > maxNotesPerRead || s.readRounds >= maxReadRounds {
		return nil, fmt.Errorf("memory read exceeds note count or round limits: %w", errInvalidProposal)
	}
	visible, _ := s.updateNoteIDs(s.updateIndexEnd())
	if len(files) == 2 && files[0] == files[1] {
		return nil, fmt.Errorf("memory read repeated a note ID: %w", errInvalidProposal)
	}
	for _, file := range files {
		if !visible[file] || s.note(file).info != nil {
			return nil, fmt.Errorf("memory read must request unread index IDs: %w", errInvalidProposal)
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

func apply(ctx context.Context, path string, s storeSnapshot, p proposal) (string, error) {
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
		for _, note := range s.notes {
			rev, err := readRevision(root, note.name, maxNote)
			if err != nil || !rev.same(note.rev) {
				return "", errStale
			}
		}
	}
	switch p.Action {
	case actionRemember:
		index, err := editIndex(s.index.text, p)
		if err != nil {
			return "", err
		}
		text := renderNote(p)
		if len(text) > maxNote {
			return "", errors.New("memory note would exceed size limit")
		}
		if root == nil {
			root, err = createStore(path)
			if err != nil {
				return "", err
			}
			defer func() { _ = root.Close() }()
		}
		if err := writeNote(root, p.File, text, revision{}); err != nil {
			return "", err
		}
		if err := writeNote(root, system.AutoMemoryIndexName, index, s.index); err != nil {
			// Leave the complete note intact rather than risk deleting a note
			// an external writer may already have seen and edited.
			return "", fmt.Errorf("memory note created but index update failed: %w", err)
		}
		return fmt.Sprintf("Memory · saved %s", p.File), nil
	case actionUpdate:
		old := s.note(p.File)
		if old.info == nil {
			return "", errInvalidProposal
		}
		text, err := patchNote(old.text, p)
		if err != nil || text == old.text {
			return "", err
		}
		if len(text) > maxNote {
			return "", errors.New("memory note would exceed size limit")
		}
		if err := writeNote(root, p.File, text, old); err != nil {
			return "", err
		}
		return fmt.Sprintf("Memory · updated %s", p.File), nil
	case actionForget:
		old := s.note(p.File)
		if old.info == nil {
			return "", errInvalidProposal
		}
		index, err := editIndex(s.index.text, p)
		if err != nil {
			return "", err
		}
		if index != s.index.text {
			if err := writeNote(root, system.AutoMemoryIndexName, index, s.index); err != nil {
				return "", err
			}
		}
		rev, err := readRevision(root, p.File, maxNote)
		if err != nil || !rev.same(old) {
			err = errStale
		} else {
			err = root.Remove(p.File)
		}
		if err != nil {
			if index != s.index.text {
				latest, rerr := readRevision(root, system.AutoMemoryIndexName, maxIndex)
				if rerr == nil && latest.text == index {
					rerr = writeNote(root, system.AutoMemoryIndexName, s.index.text, latest)
				}
				err = errors.Join(err, rerr)
			}
			return "", err
		}
		return fmt.Sprintf("Memory · forgot %s", p.File), nil
	}
	return "", errors.New("unsupported memory operation")
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
	tmp := ".memory-update-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmp) }()
	_, err = io.WriteString(f, text)
	if err == nil && old.info != nil && runtime.GOOS != "windows" {
		err = f.Chmod(old.info.Mode().Perm())
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if old.info == nil {
		return root.Link(tmp, name)
	}
	limit := maxNote
	if name == system.AutoMemoryIndexName {
		limit = maxIndex
	}
	current, err := readRevision(root, name, limit)
	if err != nil || !current.same(old) {
		return errStale
	}
	return root.Rename(tmp, name)
}
