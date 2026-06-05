package llm

import (
	"context"
	"fmt"
	"sync"

	"github.com/genai-io/san/internal/secret"
)

// registryEntry holds a provider's metadata and factory
type registryEntry struct {
	meta    Meta
	factory Factory
}

// Registry manages provider registration and discovery
type Registry struct {
	mu      sync.RWMutex
	entries map[string]registryEntry // key: "provider:authMethod"
}

// globalRegistry is the default registry instance
var globalRegistry = &Registry{
	entries: make(map[string]registryEntry),
}

// Register registers a provider with its metadata and factory
func Register(meta Meta, factory Factory) {
	globalRegistry.Register(meta, factory)
}

// Unregister removes a provider/auth-method entry from the global registry.
func Unregister(provider Name, authMethod AuthMethod) {
	globalRegistry.Unregister(provider, authMethod)
}

// Register registers a provider with its metadata and factory
func (r *Registry) Register(meta Meta, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[meta.Key()] = registryEntry{
		meta:    meta,
		factory: factory,
	}
}

// Unregister removes a provider/auth-method entry from the registry.
func (r *Registry) Unregister(provider Name, authMethod AuthMethod) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, makeProviderKey(provider, authMethod))
}

// GetProvider returns a provider instance for the given provider and auth method
func GetProvider(ctx context.Context, provider Name, authMethod AuthMethod) (Provider, error) {
	return globalRegistry.GetProvider(ctx, provider, authMethod)
}

// GetProvider returns a provider instance for the given provider and auth method
func (r *Registry) GetProvider(ctx context.Context, provider Name, authMethod AuthMethod) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[makeProviderKey(provider, authMethod)]
	if !ok {
		return nil, fmt.Errorf("provider not registered: %s:%s", provider, authMethod)
	}

	return entry.factory(ctx)
}

// GetMeta returns the metadata for a specific provider configuration
func GetMeta(provider Name, authMethod AuthMethod) (Meta, bool) {
	return globalRegistry.GetMeta(provider, authMethod)
}

// GetMeta returns the metadata for a specific provider configuration
func (r *Registry) GetMeta(provider Name, authMethod AuthMethod) (Meta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.entries[makeProviderKey(provider, authMethod)]
	if !ok {
		return Meta{}, false
	}
	return entry.meta, true
}

// makeProviderKey creates a unique key for provider and auth method combination
func makeProviderKey(provider Name, authMethod AuthMethod) string {
	return string(provider) + ":" + string(authMethod)
}

// IsReady checks if all required environment variables are set for a provider
func IsReady(meta Meta) bool {
	return globalRegistry.IsReady(meta)
}

// IsReady checks if all required environment variables are set for a provider
func (r *Registry) IsReady(meta Meta) bool {
	for _, envVar := range meta.EnvVars {
		if secret.Resolve(envVar) == "" {
			return false
		}
	}
	return true
}

// Status represents the connection status of a provider
type Status string

const (
	StatusConnected     Status = "connected"
	StatusAvailable     Status = "available"
	StatusNotConfigured Status = "not_configured"
)

// Info contains provider metadata with its current status
type Info struct {
	Meta   Meta
	Status Status
}

// GetProvidersWithStatus returns all providers grouped by provider name with their status
func GetProvidersWithStatus(store *Store) map[Name][]Info {
	return globalRegistry.GetProvidersWithStatus(store)
}

// GetProvidersWithStatus returns all providers grouped by provider name with their status
func (r *Registry) GetProvidersWithStatus(store *Store) map[Name][]Info {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[Name][]Info)

	for _, entry := range r.entries {
		var status Status
		if store.IsConnected(entry.meta.Provider, entry.meta.AuthMethod) {
			status = StatusConnected
		} else if r.IsReady(entry.meta) {
			status = StatusAvailable
		} else {
			status = StatusNotConfigured
		}

		info := Info{
			Meta:   entry.meta,
			Status: status,
		}
		result[entry.meta.Provider] = append(result[entry.meta.Provider], info)
	}

	return result
}
