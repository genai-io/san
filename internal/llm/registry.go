package llm

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
	mu              sync.RWMutex
	entries         map[string]registryEntry // key: "provider:authMethod"
	providerDisplay map[Name]ProviderDisplay // provider-level UI presentation
	costEstimators  map[Name]CostEstimator   // per-provider turn-cost pricing
	authenticators  map[string]Authenticator // key: "provider:authMethod"; interactive (OAuth) login
}

// globalRegistry is the default registry instance
var globalRegistry = &Registry{
	entries:         make(map[string]registryEntry),
	providerDisplay: make(map[Name]ProviderDisplay),
	costEstimators:  make(map[Name]CostEstimator),
	authenticators:  make(map[string]Authenticator),
}

// Register registers a provider with its metadata and factory
func Register(meta Meta, factory Factory) {
	globalRegistry.Register(meta, factory)
}

// RegisterProviderDisplay registers a provider's UI presentation (display name and order).
// Call once per provider package — typically alongside the first Register() call.
func RegisterProviderDisplay(provider Name, pd ProviderDisplay) {
	globalRegistry.RegisterProviderDisplay(provider, pd)
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

// RegisterProviderDisplay registers a provider's UI presentation.
func (r *Registry) RegisterProviderDisplay(provider Name, pd ProviderDisplay) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providerDisplay[provider] = pd
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

// ProvidersByOrder returns unique provider names sorted by their Order field.
func ProvidersByOrder() []Name {
	return globalRegistry.ProvidersByOrder()
}

// ProvidersByOrder returns unique provider names sorted by their Order field.
func (r *Registry) ProvidersByOrder() []Name {
	r.mu.RLock()
	defer r.mu.RUnlock()

	type providerOrder struct {
		name  Name
		order int
	}
	providers := make([]providerOrder, 0, len(r.providerDisplay))
	for name, pd := range r.providerDisplay {
		providers = append(providers, providerOrder{name, pd.Order})
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].order < providers[j].order
	})
	result := make([]Name, len(providers))
	for i, p := range providers {
		result[i] = p.name
	}
	return result
}

// IsProvider reports whether name is a registered provider vendor. It is the
// source of truth for telling a "vendor/model" routing ref apart from a bare
// model id that merely contains a slash.
func IsProvider(name Name) bool {
	return globalRegistry.IsProvider(name)
}

// IsProvider reports whether name is a registered provider vendor.
func (r *Registry) IsProvider(name Name) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.providerDisplay[name]
	return ok
}

// ParseVendorModel reads the explicit "vendor/model" routing form (e.g.
// "deepseek/deepseek-v4"). A ref is vendor-qualified only when the part before
// the first slash is a registered provider name; a bare model id that merely
// contains a slash (e.g. mimo's "xiaomi/mimo-v2-flash") is not, so it reports
// ok=false and the caller keeps the ref on the current provider. A known but
// unconnected vendor still parses here; resolving it is the caller's job.
func ParseVendorModel(ref string) (vendor Name, model string, ok bool) {
	v, m, found := strings.Cut(ref, "/")
	if !found || v == "" || m == "" || !IsProvider(Name(v)) {
		return "", "", false
	}
	return Name(v), m, true
}

// ProviderDisplayName returns the provider-level display name for a provider.
func ProviderDisplayName(p Name) string {
	return globalRegistry.ProviderDisplayName(p)
}

// ProviderDisplayName returns the provider-level display name for a provider.
func (r *Registry) ProviderDisplayName(p Name) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if pd, ok := r.providerDisplay[p]; ok {
		return pd.Name
	}
	return string(p)
}
