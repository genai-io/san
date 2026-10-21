package secret

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/genai-io/san/internal/atomicfile"
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
	return atomicfile.ReadJSON(s.path, &s.data)
}

// update applies fn to the file's current contents under a cross-process
// lock, so keys another San process wrote since startup survive.
func (s *Store) update(fn func(map[string]string)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return atomicfile.WithLock(s.path+".lock", func() error {
		data := map[string]string{}
		if err := atomicfile.ReadJSON(s.path, &data); err != nil {
			return err
		}
		if data == nil {
			data = map[string]string{}
		}
		fn(data)
		// 0600: this file holds API keys and tokens.
		if err := atomicfile.WriteJSON(s.path, data, 0o600); err != nil {
			return err
		}
		s.data = data
		return nil
	})
}

func (s *Store) Set(key, value string) error {
	return s.update(func(data map[string]string) { data[key] = value })
}

func (s *Store) Get(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

func (s *Store) Delete(key string) error {
	return s.update(func(data map[string]string) { delete(data, key) })
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
