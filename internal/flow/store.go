package flow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/log"
)

// Saved is a flow definition found on disk. Only the frontmatter is read
// while listing; Parse runs when one is actually launched, so a broken
// definition is reported to whoever tried to run it rather than at startup.
type Saved struct {
	Name        string
	Description string
	Path        string
}

// Load lists the `*.md` definitions in dirs, earliest directory winning when
// a name appears twice, sorted by name. Callers pass the directories in
// priority order; a missing directory is not an error.
func Load(dirs ...string) []Saved {
	var out []Saved
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			s := Saved{Name: strings.TrimSuffix(e.Name(), ".md"), Path: path}
			// Only the head is read and only the header parsed: listing a
			// directory should cost neither the whole of a file nobody is
			// running nor the graph inside it. A definition whose header does
			// not parse still lists, under its filename.
			var header Blueprint
			if _, err := header.parseFrontmatter(head(path)); err == nil {
				if header.Name != "" {
					s.Name = header.Name
				}
				s.Description = header.Description
			}
			if seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Saved) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Find returns the definition named name, parsed and validated.
func Find(name string, dirs ...string) (*Blueprint, error) {
	source, err := ReadDefinition(name, dirs...)
	if err != nil {
		return nil, err
	}
	w, err := Parse(source)
	if err != nil {
		return nil, err
	}
	if w.Name == "" {
		w.Name = name
	}
	return w, nil
}

// ReadDefinition returns the exact source of the highest-priority named definition.
func ReadDefinition(name string, dirs ...string) (string, error) {
	for _, entry := range Load(dirs...) {
		if entry.Name == name {
			data, err := os.ReadFile(entry.Path)
			return string(data), err
		}
	}
	return "", fmt.Errorf("no flow named %s: %w", name, ErrNotFound)
}

// ErrNotFound reports a name that matched no definition, so a caller can
// tell it from a definition that was found but would not parse.
var ErrNotFound = errors.New("no such flow")

// head returns the first headBytes of a file, enough for any frontmatter.
func head(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil {
		return ""
	}
	return string(b)
}

const headBytes = 4 << 10

// Serialize definition saves in this process; WithLock covers other processes.
var definitionWrites sync.Mutex
var definitionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// SaveDefinition saves a reviewed copy in the first directory. baseline is the
// source the editor opened; a changed source must be reviewed before replacement.
func SaveDefinition(name, source, baseline string, dirs ...string) error {
	if len(dirs) == 0 {
		return fmt.Errorf("flow directory is not configured")
	}
	if !definitionName.MatchString(name) {
		return fmt.Errorf("flow name must start with a letter and contain only letters, numbers, - or _")
	}
	w, err := Parse(source)
	if err != nil {
		return err
	}
	if w.Name != "" && w.Name != name {
		return fmt.Errorf("frontmatter name %q must match %q", w.Name, name)
	}

	definitionWrites.Lock()
	defer definitionWrites.Unlock()
	// The lock lives beside the run records, outside the shared definition
	// directory, and serializes the conflict check with other processes.
	return atomicfile.WithLock(filepath.Join(runDir(dirs[0]), ".definitions.lock"), func() error {
		return saveDefinition(name, source, baseline, dirs)
	})
}

// runDir keeps run records next to, not inside, the definition directory.
func runDir(definitions string) string {
	return filepath.Join(filepath.Dir(definitions), "workflow-runs")
}

func saveDefinition(name, source, baseline string, dirs []string) error {
	target := filepath.Join(dirs[0], name+".md")
	found := false
	for _, entry := range Load(dirs...) {
		if entry.Name != name {
			continue
		}
		found = true
		current, err := os.ReadFile(entry.Path)
		if err != nil {
			return err
		}
		if baseline == "" {
			return fmt.Errorf("flow %s already exists; open it for review before editing", name)
		}
		if string(current) != baseline {
			return fmt.Errorf("flow %s changed on disk; reopen it and review the changes", name)
		}
		// An existing project definition may have a filename different from
		// its frontmatter name. Replace that file, not a duplicate definition.
		if filepath.Clean(filepath.Dir(entry.Path)) == filepath.Clean(dirs[0]) {
			target = entry.Path
		}
		break
	}
	if !found && baseline != "" {
		return fmt.Errorf("flow %s was removed; reopen it before saving", name)
	}
	if data, err := os.ReadFile(target); err == nil && (!found || string(data) != baseline) {
		return fmt.Errorf("%s already contains another definition; choose a different name", target)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Definitions are project files meant to be shared, like skills.
	return atomicfile.Write(target, []byte(source), 0o644)
}

// RunRecord keeps a definition and inputs fixed across execution attempts.
// Complete outputs live in node checkpoints; live progress belongs to the
// native task view and is not persisted here.
type RunRecord struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Definition string                `json:"definition"`
	Inputs     map[string]string     `json:"inputs,omitempty"`
	Status     string                `json:"status"`
	TaskID     string                `json:"task_id,omitempty"`
	StartedAt  time.Time             `json:"started_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
	FinishedAt time.Time             `json:"finished_at,omitzero"`
	Nodes      map[string]Checkpoint `json:"nodes"`
	Error      string                `json:"error,omitempty"`
	Output     string                `json:"output,omitempty"`
}

const (
	RunRunning     = "running"
	RunSucceeded   = "succeeded"
	RunFailed      = "failed"
	RunStopped     = "stopped"
	RunInterrupted = "interrupted"
)

// maxFinishedRuns bounds the run history kept on disk. Older finished runs are
// pruned when a new one starts; running and unreadable records are never touched.
const maxFinishedRuns = 50

var storeMu sync.Mutex

// ErrRunActive means a live owner, in this or another process, holds the run.
var ErrRunActive = errors.New("run already active in another executor")
var validRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var memoryLocks = map[*Store]map[string]bool{}

// Store persists immutable-definition run snapshots. An empty directory uses memory.
type Store struct {
	dir     string
	records map[string]RunRecord
}

func NewStore(dir string) *Store { return &Store{dir: dir, records: map[string]RunRecord{}} }
func checkID(id string) error {
	if !validRunID.MatchString(id) {
		return fmt.Errorf("invalid run ID %q", id)
	}
	return nil
}
func cloneRecord(r RunRecord) RunRecord {
	b, _ := json.Marshal(r)
	var c RunRecord
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Store) get(id string) (*RunRecord, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	if s.dir == "" {
		r, ok := s.records[id]
		if !ok {
			return nil, os.ErrNotExist
		}
		c := cloneRecord(r)
		return &c, nil
	}
	b, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var r RunRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("run %s: %w", id, err)
	}
	return &r, nil
}
func (s *Store) put(r RunRecord) error {
	if err := checkID(r.ID); err != nil {
		return err
	}
	if s.dir == "" {
		s.records[r.ID] = cloneRecord(r)
		return nil
	}
	return atomicfile.WriteJSON(filepath.Join(s.dir, r.ID+".json"), r, 0600)
}
func (s *Store) Put(r RunRecord) error { storeMu.Lock(); defer storeMu.Unlock(); return s.put(r) }
func (s *Store) Get(id string) (*RunRecord, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	return s.get(id)
}
func (s *Store) Update(id string, fn func(*RunRecord)) error {
	storeMu.Lock()
	defer storeMu.Unlock()
	r, err := s.get(id)
	if err != nil {
		return err
	}
	fn(r)
	r.UpdatedAt = time.Now()
	return s.put(*r)
}

// List returns readable records, newest first. A corrupt or foreign file is
// skipped and logged so one bad record cannot hide the rest of the history.
func (s *Store) List() ([]RunRecord, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	return s.list()
}

func (s *Store) list() ([]RunRecord, error) {
	out := []RunRecord{}
	if s.dir == "" {
		for _, r := range s.records {
			out = append(out, cloneRecord(r))
		}
	} else {
		entries, err := os.ReadDir(s.dir)
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if e.IsDir() || !ok {
				continue
			}
			r, err := s.get(id)
			if err != nil {
				log.Logger().Warn("flow: skipping unreadable run record", zap.String("file", e.Name()), zap.Error(err))
				continue
			}
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// FindByTask returns the run whose latest execution is the given task.
func (s *Store) FindByTask(taskID string) (*RunRecord, error) {
	records, err := s.List()
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.TaskID == taskID {
			return &r, nil
		}
	}
	return nil, os.ErrNotExist
}

// Prune deletes the oldest finished runs beyond keep. A run is deleted only
// while its lock is held, so an active or resuming run is never removed.
func (s *Store) Prune(keep int) error {
	records, err := s.List()
	if err != nil {
		return err
	}
	var errs []error
	finished := 0
	for _, r := range records {
		if r.Status == RunRunning {
			continue
		}
		if finished++; finished <= keep {
			continue
		}
		release, err := s.Acquire(r.ID)
		if errors.Is(err, ErrRunActive) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		storeMu.Lock()
		if s.dir == "" {
			delete(s.records, r.ID)
		} else if err := os.Remove(filepath.Join(s.dir, r.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		storeMu.Unlock()
		release()
		if s.dir != "" {
			// After release; a concurrent opener of the removed inode then
			// finds no record and refuses to run.
			_ = os.Remove(filepath.Join(s.dir, r.ID+".lock"))
		}
	}
	return errors.Join(errs...)
}

// Acquire excludes concurrent executors, including other processes. The lock
// is an operating-system file lock, released automatically when its holder
// exits, so a crashed owner never blocks recovery.
func (s *Store) Acquire(id string) (func(), error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	if s.dir == "" {
		storeMu.Lock()
		defer storeMu.Unlock()
		if memoryLocks[s] == nil {
			memoryLocks[s] = map[string]bool{}
		}
		if memoryLocks[s][id] {
			return nil, fmt.Errorf("run %s: %w", id, ErrRunActive)
		}
		memoryLocks[s][id] = true
		var once sync.Once
		return func() {
			once.Do(func() {
				storeMu.Lock()
				delete(memoryLocks[s], id)
				if len(memoryLocks[s]) == 0 {
					delete(memoryLocks, s)
				}
				storeMu.Unlock()
			})
		}, nil
	}
	release, err := atomicfile.TryLock(filepath.Join(s.dir, id+".lock"))
	if errors.Is(err, atomicfile.ErrLocked) {
		return nil, fmt.Errorf("run %s: %w", id, ErrRunActive)
	}
	return release, err
}
