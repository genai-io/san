package llm

import (
	"context"
	"fmt"
	"sync"
)

// ProviderPool hands out a live Provider for a connected vendor, opening each
// vendor's connection once and reusing it. A session that routes work across
// vendors — say a planner on Anthropic and a coder on DeepSeek — shares one
// pool so every subagent on the same vendor talks through the same client.
type ProviderPool struct {
	store *Store
	mu    sync.Mutex
	byKey map[string]Provider // makeProviderKey(vendor, authMethod)
}

// NewProviderPool returns a pool backed by the given connection store.
func NewProviderPool(store *Store) *ProviderPool {
	return &ProviderPool{store: store, byKey: make(map[string]Provider)}
}

// Resolve returns the provider for a connected vendor (e.g. "deepseek").
//
// The auth method comes from how the user connected that vendor, so a model
// family served via Vertex or Bedrock resolves like a direct API key — the
// "vendor/model" routing form names the vendor, never the serving platform.
// Resolution fails when the vendor is not connected; it never falls back to a
// different vendor.
func (p *ProviderPool) Resolve(ctx context.Context, vendor Name) (Provider, error) {
	if p == nil || p.store == nil {
		return nil, fmt.Errorf("provider pool is not configured")
	}

	conn, ok := p.store.GetConnection(vendor)
	if !ok {
		return nil, fmt.Errorf("provider %q is not connected", vendor)
	}
	key := makeProviderKey(vendor, conn.AuthMethod)

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.byKey[key]; ok {
		return existing, nil
	}
	provider, err := GetProvider(ctx, vendor, conn.AuthMethod)
	if err != nil {
		return nil, err
	}
	p.byKey[key] = provider
	return provider, nil
}
