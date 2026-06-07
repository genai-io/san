package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genai-io/san/internal/confdir"
)

const (
	// modelCacheTTL is the time-to-live for cached models
	modelCacheTTL = 24 * time.Hour
)

// ConnectionInfo stores connection information for a provider
type ConnectionInfo struct {
	AuthMethod  AuthMethod `json:"authMethod"`
	ConnectedAt time.Time  `json:"connectedAt"`
}

// modelCache stores cached model information
type modelCache struct {
	CachedAt time.Time   `json:"cachedAt"`
	Models   []ModelInfo `json:"models"`
}

// CurrentModelInfo stores the current model with its provider info
type CurrentModelInfo struct {
	ModelID    string     `json:"modelId"`
	Provider   Name       `json:"provider"`
	AuthMethod AuthMethod `json:"authMethod"`
}

// tokenLimitOverride stores custom token limits for a model
type tokenLimitOverride struct {
	InputTokenLimit  int `json:"inputTokenLimit"`
	OutputTokenLimit int `json:"outputTokenLimit"`
}

// storeData is the persisted data structure
type storeData struct {
	Connections     map[string]ConnectionInfo     `json:"connections"`               // key: provider
	Models          map[string]modelCache         `json:"models"`                    // key: provider:authMethod
	Current         *CurrentModelInfo             `json:"current"`                   // current model with provider info
	SearchProvider  *string                       `json:"searchProvider,omitempty"`  // search provider name (exa, serper, brave)
	TokenLimits     map[string]tokenLimitOverride `json:"tokenLimits,omitempty"`     // key: modelID
	ThinkingEfforts map[string]string             `json:"thinkingEfforts,omitempty"` // key: modelID; value: provider-native effort label
}

// Store manages provider configuration persistence
type Store struct {
	mu   sync.RWMutex
	path string
	data storeData
}

// NewStore creates a new Store instance
func NewStore() (*Store, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configDir := confdir.Dir(homeDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, err
	}

	store := &Store{
		path: filepath.Join(configDir, "providers.json"),
		data: storeData{
			Connections: make(map[string]ConnectionInfo),
			Models:      make(map[string]modelCache),
		},
	}

	// Load existing data if available
	if err := store.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return store, nil
}

// load reads the store data from disk
func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("read provider store %s: %w", s.path, err)
	}

	if err := json.Unmarshal(data, &s.data); err != nil {
		return fmt.Errorf("parse provider store: %w", err)
	}

	// Initialize maps if nil
	s.ensureMapsInitialized()
	return nil
}

// ensureMapsInitialized ensures all map fields are non-nil
func (s *Store) ensureMapsInitialized() {
	if s.data.Connections == nil {
		s.data.Connections = make(map[string]ConnectionInfo)
	}
	if s.data.Models == nil {
		s.data.Models = make(map[string]modelCache)
	}
	if s.data.TokenLimits == nil {
		s.data.TokenLimits = make(map[string]tokenLimitOverride)
	}
	if s.data.ThinkingEfforts == nil {
		s.data.ThinkingEfforts = make(map[string]string)
	}
}

// save writes the store data to disk
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write provider store %s: %w", s.path, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename provider store %s: %w", s.path, err)
	}
	return nil
}

// Connect saves a connection for a provider
func (s *Store) Connect(provider Name, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Connections[string(provider)] = ConnectionInfo{
		AuthMethod:  authMethod,
		ConnectedAt: time.Now(),
	}

	return s.save()
}

// IsConnected checks if a provider is connected with the specified auth method
func (s *Store) IsConnected(provider Name, authMethod AuthMethod) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, ok := s.data.Connections[string(provider)]
	if !ok {
		return false
	}
	return conn.AuthMethod == authMethod
}

// GetConnection returns the connection info for a provider
func (s *Store) GetConnection(provider Name) (ConnectionInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conn, ok := s.data.Connections[string(provider)]
	return conn, ok
}

// GetConnections returns all connections
func (s *Store) GetConnections() map[string]ConnectionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]ConnectionInfo, len(s.data.Connections))
	maps.Copy(result, s.data.Connections)
	return result
}

// Disconnect removes the connection for a provider.
func (s *Store) Disconnect(provider Name) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data.Connections, string(provider))
	return s.save()
}

// RemoveCachedModels removes cached models for a provider and auth method.
func (s *Store) RemoveCachedModels(provider Name, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data.Models, makemodelCacheKey(provider, authMethod))
	return s.save()
}

// ClearCurrentModel clears the current model selection.
func (s *Store) ClearCurrentModel() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Current = nil
	return s.save()
}

// CacheModels saves model information for a provider.
func (s *Store) CacheModels(provider Name, authMethod AuthMethod, models []ModelInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := makemodelCacheKey(provider, authMethod)
	s.data.Models[key] = modelCache{
		CachedAt: time.Now(),
		Models:   models,
	}

	return s.save()
}

// GetCachedModels returns cached models if they exist and are not expired
func (s *Store) GetCachedModels(provider Name, authMethod AuthMethod) ([]ModelInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cache, ok := s.data.Models[makemodelCacheKey(provider, authMethod)]
	if !ok {
		return nil, false
	}
	if time.Since(cache.CachedAt) > modelCacheTTL {
		return nil, false
	}

	return cache.Models, true
}

// makemodelCacheKey creates a cache key for provider and auth method
func makemodelCacheKey(provider Name, authMethod AuthMethod) string {
	return string(provider) + ":" + string(authMethod)
}

// GetAllCachedModels returns all cached models grouped by provider key
func (s *Store) GetAllCachedModels() map[string][]ModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]ModelInfo)
	for key, cache := range s.data.Models {
		if time.Since(cache.CachedAt) > modelCacheTTL {
			continue
		}
		result[key] = cache.Models
	}
	return result
}

// GetAllCachedModelsIncludeExpired returns all cached models regardless of TTL.
// Used to show stale data immediately rather than blocking the UI.
func (s *Store) GetAllCachedModelsIncludeExpired() map[string][]ModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]ModelInfo)
	for key, cache := range s.data.Models {
		if len(cache.Models) > 0 {
			result[key] = cache.Models
		}
	}
	return result
}

// CachedModelDisplayName returns the display name for a model ID found in any
// cached provider list, ignoring TTL. Returns "" if the ID isn't cached.
//
// The same model can be cached under several provider/auth keys (e.g. a model
// offered both directly and via an aggregator). One provider may list a real
// display name ("DeepSeek V4 Pro") while another only echoes the raw ID
// ("deepseek-v4-pro"). Returning whichever entry we hit first would make the
// status bar flicker between the two, because Go randomizes map iteration
// order between renders. So we prefer a real display name — one that differs
// from the ID — and only fall back to the raw name/ID when no real name
// exists. Scans in place without allocating, since it runs on every render.
func (s *Store) CachedModelDisplayName(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw := "" // the raw ID echoed back as a name; used only if no real name is found
	for _, cache := range s.data.Models {
		for _, m := range cache.Models {
			if m.ID != id {
				continue
			}
			name := m.DisplayName
			if name == "" {
				name = m.Name
			}
			if name != "" && name != id {
				return name // a real, human-readable display name
			}
			raw = name // keep scanning in case another provider has a real name
		}
	}
	return raw
}

// SetCurrentModel sets the current model with provider info
func (s *Store) SetCurrentModel(modelID string, provider Name, authMethod AuthMethod) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.Current = &CurrentModelInfo{
		ModelID:    modelID,
		Provider:   provider,
		AuthMethod: authMethod,
	}
	return s.save()
}

// GetCurrentModel returns the current model info
func (s *Store) GetCurrentModel() *CurrentModelInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.data.Current
}

// GetSearchProvider returns the current search provider name
func (s *Store) GetSearchProvider() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.data.SearchProvider == nil {
		return "" // Will use default (exa)
	}
	return *s.data.SearchProvider
}

// SetSearchProvider sets the search provider
func (s *Store) SetSearchProvider(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.SearchProvider = &name
	return s.save()
}

// SetTokenLimit sets custom token limits for a model.
// It also updates the model cache so subsequent model listings reflect these limits.
func (s *Store) SetTokenLimit(modelID string, inputLimit, outputLimit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureMapsInitialized()
	s.data.TokenLimits[modelID] = tokenLimitOverride{
		InputTokenLimit:  inputLimit,
		OutputTokenLimit: outputLimit,
	}

	// Update the model cache entry so model listings show the limits.
	// We copy the slice before modifying to avoid mutating arrays shared with
	// callers that received a slice from GetCachedModels.
	for key, cache := range s.data.Models {
		modified := false
		for _, m := range cache.Models {
			if m.ID == modelID {
				modified = true
				break
			}
		}
		if !modified {
			continue
		}
		newModels := make([]ModelInfo, len(cache.Models))
		copy(newModels, cache.Models)
		for i := range newModels {
			if newModels[i].ID == modelID {
				newModels[i].InputTokenLimit = inputLimit
				newModels[i].OutputTokenLimit = outputLimit
			}
		}
		cache.Models = newModels
		s.data.Models[key] = cache
	}

	return s.save()
}

// GetThinkingEffort returns the persisted thinking effort for modelID,
// or "" when no preference has been saved (fall back to provider default).
func (s *Store) GetThinkingEffort(modelID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.ThinkingEfforts[modelID]
}

// SetThinkingEffort saves the thinking effort for modelID.
// Passing "" deletes the entry so future loads fall back to the provider default.
func (s *Store) SetThinkingEffort(modelID, effort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureMapsInitialized()
	if effort == "" {
		delete(s.data.ThinkingEfforts, modelID)
	} else {
		s.data.ThinkingEfforts[modelID] = effort
	}
	return s.save()
}

// GetTokenLimit returns custom token limits for a model
func (s *Store) GetTokenLimit(modelID string) (inputLimit, outputLimit int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	override, exists := s.data.TokenLimits[modelID]
	if !exists {
		return 0, 0, false
	}
	return override.InputTokenLimit, override.OutputTokenLimit, true
}
