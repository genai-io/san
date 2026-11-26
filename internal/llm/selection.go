package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/genai-io/san/internal/setting"
)

// ResolveProviderModel selects a connected provider and concrete model for noninteractive hosts.
func ResolveProviderModel(ctx context.Context) (Provider, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	store, err := NewStoreForCwd(cwd)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load store: %w", err)
	}
	resolved, ok := ResolveProvider(ctx, store)
	if !ok {
		return nil, "", fmt.Errorf("no provider connected. Run 'san' and use /models to connect")
	}
	model := resolved.ModelID
	if model == "" {
		name, _, _ := strings.Cut(resolved.Provider.Name(), ":")
		model = setting.DefaultModel(name, string(resolved.AuthMethod))
	}
	if model == "" {
		return nil, "", fmt.Errorf("no model selected for %s. Run 'san' and use /models to select one", resolved.Provider.Name())
	}
	return resolved.Provider, model, nil
}
