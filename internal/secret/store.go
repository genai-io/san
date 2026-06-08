package secret

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/genai-io/san/internal/confdir"
)

type Store struct {
	mu   sync.RWMutex
	path string
	data map[string]string
}

var (
	defaultStore *Store
	defaultOnce  sync.Once
)

func Default() *Store {
	defaultOnce.Do(func() {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return
		}
		configDir := confdir.Dir(homeDir)
		_ = os.MkdirAll(configDir, 0o755)
		defaultStore = &Store{
			path: filepath.Join(configDir, "secrets.json"),
			data: make(map[string]string),
		}
		_ = defaultStore.load()
	})
	return defaultStore
}

// ResetDefault discards the cached default store so the next Default() call
// re-resolves it against the current HOME. Intended for tests that isolate the
// secret store via t.Setenv("HOME", t.TempDir()); without it the sync.Once in
// Default() would pin the path to the real ~/.san on first use.
func ResetDefault() {
	defaultStore = nil
	defaultOnce = sync.Once{}
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return json.Unmarshal(raw, &s.data)
}

func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return s.save()
}

func (s *Store) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return s.save()
}

// ResolveEnv returns the value for an environment variable name,
// checking os.Getenv first, then falling back to the stored value.
func (s *Store) ResolveEnv(envVar string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return s.Get(envVar)
}

// Resolve is a standalone helper that uses the default store.
func Resolve(envVar string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if s := Default(); s != nil {
		return s.Get(envVar)
	}
	return ""
}
