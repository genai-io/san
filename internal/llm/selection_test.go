package llm

import (
	"context"
	"testing"
)

type selectionProvider struct {
	Provider
	name string
}

func (p selectionProvider) Name() string { return p.name }

func TestResolveProviderModelFallback(t *testing.T) {
	for _, tc := range []struct{ name, model, message string }{
		{"", "", "no provider connected. Run 'san' and use /models to connect"},
		{"anthropic:vertex", "claude-sonnet-4-5@20250929", ""},
		{"custom:vertex", "", "no model selected for custom:vertex. Run 'san' and use /models to select one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cwd := t.TempDir()
			t.Chdir(cwd)
			store, err := NewStoreForCwd(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name != "" {
				const id ProviderID = "selection-test"
				const auth AuthMethod = "vertex"
				Register(Meta{Provider: id, AuthMethod: auth}, func(context.Context) (Provider, error) { return selectionProvider{name: tc.name}, nil })
				t.Cleanup(func() { Unregister(id, auth) })
				if err := store.Connect(id, auth); err != nil {
					t.Fatal(err)
				}
			}
			p, model, err := ResolveProviderModel(t.Context())
			if tc.message != "" {
				if err == nil || err.Error() != tc.message || p != nil || model != "" {
					t.Fatalf("got %v, %q, %v", p, model, err)
				}
				return
			}
			if err != nil || p == nil || p.Name() != tc.name || model != tc.model {
				t.Fatalf("got %v, %q, %v", p, model, err)
			}
		})
	}
}
