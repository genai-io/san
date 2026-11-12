package llm

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
	"github.com/genai-io/san/internal/setting"
)

// What the user connected, and what they chose.
//
// The user store is providers.json in San's config directory. It holds
// connections, the default model, and per-model preferences (token limits,
// thinking effort). A project's providers.json overrides only the current
// model. Provider-supplied metadata lives in modelcache.go.
//
// Several Stores are open at once — the app has one, the /models picker opens
// its own — so a write lands on disk and the others catch up via Reload.

// ConnectionInfo records that a provider is connected, and by which method.
type ConnectionInfo struct {
	AuthMethod  AuthMethod `json:"authMethod"`
	ConnectedAt time.Time  `json:"connectedAt"`
}

// CurrentModelInfo names the model San is inferring through. The provider and
// auth method travel with the ID because the same ID can be served by several
// of them, with different windows and different pricing.
type CurrentModelInfo struct {
	ModelID    string     `json:"modelId"`
	Provider   ProviderID `json:"provider"`
	AuthMethod AuthMethod `json:"authMethod"`
}

// The user-defined OpenAI-compatible provider.
//
// There is one of it, and its ID is fixed: a user-chosen name would add rename
// bookkeeping without distinguishing anything. The two names below are what
// identify it — in the store, in the secret store, and in the Providers tab —
// and they live here beside the config they key rather than in whichever
// package happens to open the connection.
const (
	// CustomProvider is the provider name the user-defined endpoint is
	// registered and stored under.
	CustomProvider ProviderID = "custom"
	// CustomAPIKeyEnvVar is where its credential is kept.
	CustomAPIKeyEnvVar = "SAN_CUSTOM_API_KEY"
)

// CustomProviderConfig stores the user-defined OpenAI-compatible provider added
// via the /models Providers tab. The API key is not kept here — it lives in the
// secret store under CustomAPIKeyEnvVar.
type CustomProviderConfig struct {
	ID      string `json:"id"`
	BaseURL string `json:"baseURL"`
}

// storeData is providers.json, as written.
type storeData struct {
	Connections     map[string]ConnectionInfo `json:"connections"`               // key: provider
	Models          map[string]modelCache     `json:"models"`                    // key: provider:authMethod
	Current         *CurrentModelInfo         `json:"current"`                   // current model with provider info
	SearchProvider  *string                   `json:"searchProvider,omitempty"`  // search provider name (exa, serper, brave)
	ContextLimits   map[string]contextLimit   `json:"tokenLimits,omitempty"`     // key: modelID
	ThinkingEfforts map[string]string         `json:"thinkingEfforts,omitempty"` // key: modelID; value: provider-native effort label
	CustomProvider  *CustomProviderConfig     `json:"customProvider,omitempty"`  // user-defined OpenAI-compatible provider
}

// modelSelection is the project store; connections and preferences stay user-wide.
type modelSelection struct {
	Current *CurrentModelInfo `json:"current"`
}

// Store holds user preferences and an optional project model override.
// Every accessor locks; the data is unexported.
type Store struct {
	mu             sync.RWMutex
	path           string
	projectPath    string
	data           storeData
	projectCurrent *CurrentModelInfo
}

// NewStore opens the user store without a project override.
func NewStore() (*Store, error) {
	return NewStoreForCwd("")
}

// NewStoreForCwd opens the user store and the current checkout's model override.
// Outside Git, the project is cwd itself. Missing files are a first run.
func NewStoreForCwd(cwd string) (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	if real, err := filepath.EvalSymlinks(homeDir); err == nil {
		homeDir = real
	}
	configDir := confdir.Dir(homeDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}

	store := &Store{path: filepath.Join(configDir, "providers.json")}
	if cwd != "" {
		store.projectPath, err = projectModelPath(cwd)
		if err != nil {
			return nil, err
		}
		if store.projectPath == store.path {
			store.projectPath = ""
		}
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func projectModelPath(cwd string) (string, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	return filepath.Join(confdir.Dir(confdir.RepoRoot(cwd)), "providers.json"), nil
}

// ProjectModelFindings names repository-supplied routing that waits for trust.
// Untracked model files are personal choices, like other local configuration.
func ProjectModelFindings(cwd string) []string {
	path, err := projectModelPath(cwd)
	if err != nil || setting.UntrackedInRepo(path) {
		return nil
	}
	var selection modelSelection
	if atomicfile.ReadJSON(path, &selection) != nil || selection.Current == nil {
		return nil
	}
	current := selection.Current
	file, _ := filepath.Rel(cwd, path)
	return []string{fmt.Sprintf("send prompts to %s (%s, %s)  (%s)", current.ModelID, current.Provider, current.AuthMethod, file)}
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// loadLocked replaces both snapshots, including fields removed on disk.
func (s *Store) loadLocked() error {
	var data storeData
	if err := atomicfile.ReadJSON(s.path, &data); err != nil {
		return err
	}
	var project modelSelection
	if s.projectPath != "" && (setting.IsWorkspaceTrusted(filepath.Dir(filepath.Dir(s.projectPath))) || setting.UntrackedInRepo(s.projectPath)) {
		if err := atomicfile.ReadJSON(s.projectPath, &project); err != nil {
			return err
		}
	}
	s.data, s.projectCurrent = data, project.Current
	s.initMaps()
	return nil
}

// Reload re-reads the store from disk, refreshing this instance's in-memory
// caches with data written by another Store instance.
//
// The provider selector operates on its own Store (a separate NewStore), so the
// model metadata it caches — display names and context-window limits — and the
// current-model choice it persists land on disk but not in the shared app-level
// Store the status bar reads. Reloading after a model switch lets the status bar
// pick up the new model's name and limit instead of falling back to the raw ID
// and "--". A missing file is not an error: nothing has been persisted yet.
func (s *Store) Reload() error {
	return s.load()
}

// initMaps fills in the maps an older file — or a hand-edited one — may not
// have, so every writer can assign without checking first.
func (s *Store) initMaps() {
	if s.data.Connections == nil {
		s.data.Connections = make(map[string]ConnectionInfo)
	}
	if s.data.Models == nil {
		s.data.Models = make(map[string]modelCache)
	}
	if s.data.ContextLimits == nil {
		s.data.ContextLimits = make(map[string]contextLimit)
	}
	if s.data.ThinkingEfforts == nil {
		s.data.ThinkingEfforts = make(map[string]string)
	}
}

// save writes user metadata without reverting a default selected by another
// Store. Model selection writes use their own read-modify-write path.
func (s *Store) save() error {
	return atomicfile.WithLock(s.path+".lock", func() error {
		var latest modelSelection
		if err := atomicfile.ReadJSON(s.path, &latest); err != nil {
			return err
		}
		s.data.Current = latest.Current
		return atomicfile.WriteJSON(s.path, s.data, 0o644)
	})
}

// Connect records that a provider is connected by this auth method.
func (s *Store) Connect(provider ProviderID, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Connections[string(provider)] = ConnectionInfo{
		AuthMethod:  authMethod,
		ConnectedAt: time.Now(),
	}

	return s.save()
}

// IsConnected reports whether a provider is connected by this auth method.
func (s *Store) IsConnected(provider ProviderID, authMethod AuthMethod) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, ok := s.data.Connections[string(provider)]
	if !ok {
		return false
	}
	return conn.AuthMethod == authMethod
}

// Connection returns a provider's connection, if it has one.
func (s *Store) Connection(provider ProviderID) (ConnectionInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, ok := s.data.Connections[string(provider)]
	return conn, ok
}

// ResolveAuthMethod returns the model's own auth method, falling back to its
// provider's stored connection when the model doesn't carry one. Provider-scoped
// cache lookups (token limits, reasoning) key on provider+auth, so they resolve
// the auth this way to avoid missing the cache for a model selected without an
// explicit method.
func (s *Store) ResolveAuthMethod(current *CurrentModelInfo) AuthMethod {
	if current == nil {
		return ""
	}
	if current.AuthMethod != "" {
		return current.AuthMethod
	}
	return s.ConnectionAuthMethod(current.Provider)
}

// ConnectionAuthMethod returns the auth method of a provider's active
// connection, or "" when it has none. Nil-receiver safe, for callers that hold
// a provider but no model (llm.Client resolving its own context window).
func (s *Store) ConnectionAuthMethod(provider ProviderID) AuthMethod {
	if s == nil {
		return ""
	}
	if conn, ok := s.Connection(provider); ok {
		return conn.AuthMethod
	}
	return ""
}

// Connections returns a copy of every connection, keyed by provider.
func (s *Store) Connections() map[string]ConnectionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]ConnectionInfo, len(s.data.Connections))
	maps.Copy(result, s.data.Connections)
	return result
}

// Disconnect removes the connection for a provider.
func (s *Store) Disconnect(provider ProviderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data.Connections, string(provider))
	return s.save()
}

// ClearCurrentModel removes the effective selection, revealing the user default
// when a project override is cleared.
func (s *Store) ClearCurrentModel() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.loadLocked(); err != nil {
		return err
	}
	if s.projectCurrent != nil {
		return s.saveProjectModel(nil)
	}
	return s.saveUserModel(nil)
}

// SetCurrentModel seeds an unset user default; later choices apply only to the
// project. A user-only store always writes the user default.
func (s *Store) SetCurrentModel(modelID string, provider ProviderID, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Another store may have established the user default since this one opened.
	if err := s.loadLocked(); err != nil {
		return err
	}
	current := &CurrentModelInfo{
		ModelID:    modelID,
		Provider:   provider,
		AuthMethod: authMethod,
	}
	if s.data.Current != nil && s.projectPath != "" {
		return s.saveProjectModel(current)
	}

	return atomicfile.WithLock(s.path+".lock", func() error {
		// Recheck first-run scope after waiting for another store's write.
		if err := s.loadLocked(); err != nil {
			return err
		}
		if s.data.Current != nil && s.projectPath != "" {
			return s.saveProjectModel(current)
		}
		if err := s.saveUserModelLocked(current); err != nil {
			return err
		}
		if s.projectCurrent != nil {
			return s.saveProjectModel(nil)
		}
		return nil
	})
}

// SetUserDefaultModel changes only the user default, preserving any project override.
func (s *Store) SetUserDefaultModel(modelID string, provider ProviderID, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveUserModel(&CurrentModelInfo{ModelID: modelID, Provider: provider, AuthMethod: authMethod})
}

// saveUserModel preserves the latest user metadata. Caller holds the store lock.
func (s *Store) saveUserModel(current *CurrentModelInfo) error {
	return atomicfile.WithLock(s.path+".lock", func() error {
		return s.saveUserModelLocked(current)
	})
}

// saveUserModelLocked requires both the store mutex and the file lock.
func (s *Store) saveUserModelLocked(current *CurrentModelInfo) error {
	var next storeData
	if err := atomicfile.ReadJSON(s.path, &next); err != nil {
		return err
	}
	next.Current = current
	if err := atomicfile.WriteJSON(s.path, next, 0o644); err != nil {
		return err
	}
	s.data = next
	s.initMaps()
	return nil
}

// UserDefaultModel returns a copy of the loaded user default, or nil.
func (s *Store) UserDefaultModel() *CurrentModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.Current == nil {
		return nil
	}
	current := *s.data.Current
	return &current
}

// HasProjectModel reports whether a loaded project override is effective.
func (s *Store) HasProjectModel() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projectCurrent != nil
}

// saveProjectModel commits memory only after persistence succeeds. Caller locks.
func (s *Store) saveProjectModel(current *CurrentModelInfo) error {
	var existing modelSelection
	if err := atomicfile.ReadJSON(s.projectPath, &existing); err != nil {
		return err
	}
	if err := atomicfile.WriteJSON(s.projectPath, modelSelection{Current: current}, 0o644); err != nil {
		return err
	}
	s.projectCurrent = current
	return nil
}

// CurrentModel returns the project model, falling back to the user default.
func (s *Store) CurrentModel() *CurrentModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := s.projectCurrent
	if current == nil {
		current = s.data.Current
	}
	if current == nil {
		return nil
	}
	copy := *current
	return &copy
}

// SearchProvider returns the chosen web-search backend, or "" for the default.
func (s *Store) SearchProvider() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.SearchProvider == nil {
		return "" // Will use default (exa)
	}
	return *s.data.SearchProvider
}

// SetSearchProvider records the web-search backend to use.
func (s *Store) SetSearchProvider(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.SearchProvider = &name
	return s.save()
}

// ThinkingEffort returns the persisted thinking effort for modelID,
// or "" when no preference has been saved (fall back to provider default).
func (s *Store) ThinkingEffort(modelID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ThinkingEfforts[modelID]
}

// SetThinkingEffort saves the thinking effort for modelID.
// Passing "" deletes the entry so future loads fall back to the provider default.
func (s *Store) SetThinkingEffort(modelID, effort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initMaps()
	if effort == "" {
		delete(s.data.ThinkingEfforts, modelID)
	} else {
		s.data.ThinkingEfforts[modelID] = effort
	}
	return s.save()
}

// CustomProvider returns the stored custom provider config, or nil when the
// user hasn't defined one. Returns a copy so callers can't mutate the store.
func (s *Store) CustomProvider() *CustomProviderConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.CustomProvider == nil {
		return nil
	}
	cfg := *s.data.CustomProvider
	return &cfg
}

// SetCustomProvider saves the custom provider config.
func (s *Store) SetCustomProvider(cfg CustomProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.CustomProvider = &cfg
	return s.save()
}
