package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
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
