package workflow

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
)

// Saved is a workflow definition found on disk. Only the frontmatter is read
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
			var header Workflow
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
func Find(name string, dirs ...string) (*Workflow, error) {
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
	return "", fmt.Errorf("no workflow named %s: %w", name, ErrNotFound)
}

// ErrNotFound reports a name that matched no definition, so a caller can
// tell it from a definition that was found but would not parse.
var ErrNotFound = errors.New("no such workflow")

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

// Serialize definition saves without coupling them to running executions.
var definitionWrites sync.Mutex
var definitionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// SaveDefinition saves a reviewed copy in the first directory. baseline is the
// source the editor opened; a changed source must be reviewed before replacement.
func SaveDefinition(name, source, baseline string, dirs ...string) error {
	if len(dirs) == 0 {
		return fmt.Errorf("workflow directory is not configured")
	}
	if !definitionName.MatchString(name) {
		return fmt.Errorf("workflow name must start with a letter and contain only letters, numbers, - or _")
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
			return fmt.Errorf("workflow %s already exists; open it for review before editing", name)
		}
		if string(current) != baseline {
			return fmt.Errorf("workflow %s changed on disk; reopen it and review the changes", name)
		}
		// An existing project definition may have a filename different from
		// its frontmatter name. Replace that file, not a duplicate definition.
		if filepath.Clean(filepath.Dir(entry.Path)) == filepath.Clean(dirs[0]) {
			target = entry.Path
		}
		break
	}
	if !found && baseline != "" {
		return fmt.Errorf("workflow %s was removed; reopen it before saving", name)
	}
	if data, err := os.ReadFile(target); err == nil && (!found || string(data) != baseline) {
		return fmt.Errorf("%s already contains another definition; choose a different name", target)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return atomicfile.Write(target, []byte(source), 0o600)
}

// RunRecord keeps a definition and inputs fixed across execution attempts.
// Activity is a bounded live tail; complete outputs remain in checkpoints.
type RunRecord struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Definition string                `json:"definition"`
	Inputs     map[string]string     `json:"inputs,omitempty"`
	Status     string                `json:"status"`
	TaskID     string                `json:"task_id,omitempty"`
	StartedAt  time.Time             `json:"started_at"`
	UpdatedAt  time.Time             `json:"updated_at"`
	FinishedAt time.Time             `json:"finished_at,omitempty"`
	Nodes      map[string]Checkpoint `json:"nodes"`
	Activity   map[string][]string   `json:"activity,omitempty"`
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

var storeMu sync.Mutex

// ErrRunActive means a live or uncertain owner prevents execution.
var ErrRunActive = errors.New("run already active or owner liveness uncertain")
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
		return nil, err
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
func (s *Store) List() ([]RunRecord, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
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
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			r, err := s.get(e.Name()[:len(e.Name())-5])
			if err != nil {
				return nil, err
			}
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// Acquire excludes concurrent executors, including other processes. Unknown owners
// are never stolen. Only a positively dead PID permits stale lock recovery.
func (s *Store) Acquire(id string) (func(), error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	if err := checkID(id); err != nil {
		return nil, err
	}
	if s.dir == "" {
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
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, err
	}
	p := filepath.Join(s.dir, id+".lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, err = f.WriteString(strconv.Itoa(os.Getpid()))
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(p)
				return nil, err
			}
			var once sync.Once
			return func() { once.Do(func() { storeMu.Lock(); _ = os.Remove(p); storeMu.Unlock() }) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		pid, parseErr := strconv.Atoi(string(b))
		if readErr != nil || parseErr != nil || !processDead(pid) {
			return nil, fmt.Errorf("run %s: %w", id, ErrRunActive)
		}
		// Serialize stale recovery and recheck under that guard: another
		// process may have replaced the dead owner's lock since our read.
		guard, guardErr := os.OpenFile(p+".recovery", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if guardErr != nil {
			if errors.Is(guardErr, os.ErrExist) {
				return nil, fmt.Errorf("run %s recovery uncertain: %w", id, ErrRunActive)
			}
			return nil, fmt.Errorf("run %s recovery busy: %w", id, guardErr)
		}
		_ = guard.Close()
		b, readErr = os.ReadFile(p)
		pid, parseErr = strconv.Atoi(string(b))
		if readErr != nil || parseErr != nil || !processDead(pid) {
			_ = os.Remove(p + ".recovery")
			return nil, fmt.Errorf("run %s: %w", id, ErrRunActive)
		}
		err = os.Remove(p)
		_ = os.Remove(p + ".recovery")
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("run %s lock unavailable", id)
}
