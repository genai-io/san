package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/endpoint"
)

func TestCodexSolFallbackEffort(t *testing.T) {
	data := embeddedLineups("data/models.dev.json")
	data.overlay(embeddedLineups("data/san.json"))
	models := vendorModels{vendor: vendorFor(t, codexVendor), lineup: data[codexVendor]}

	const sol = "gpt-6.2-sol"
	want := []ai.Effort{ai.EffortLow, ai.EffortMedium, ai.EffortHigh, ai.EffortXHigh, ai.EffortMax}
	for name, model := range map[string]ai.Model{
		"before discovery":                   models.model(sol),
		"listing without reasoning metadata": models.resolve(ai.Model{ID: sol}),
	} {
		if got := model.Efforts(); !slices.Equal(got, want) {
			t.Errorf("%s: efforts = %v, want %v", name, got, want)
		}
		if level, ok := model.ResolveLevel(ai.EffortLow); !ok || level.Value != "low" {
			t.Errorf("%s: lowest wire effort = %+v, want low", name, level)
		}
	}
	if got := models.model("unlisted-codex-model").Efforts(); !slices.Contains(got, ai.EffortOff) {
		t.Errorf("Sol override removed off from another model: %v", got)
	}
}

func TestCodexCatalogKeepsLiveCapabilities(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("catalog request = %s %s, want GET /models", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("client_version"); got != "0.169.1" {
			t.Errorf("client_version = %q, want the explicit override", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// A model absent from the built-in snapshot must still reach the picker
		// with the limits and reasoning ladder advertised by the live catalog.
		fmt.Fprint(w, `{"models":[
			{"slug":"new-codex-model","display_name":"New Codex Model","description":"Fast coding and reasoning.","context_window":123456,
			 "default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"ultra"}]},
			{"slug":"hidden-model","show_in_picker":false},
			{"slug":""}
		]}`)
	}))
	defer server.Close()

	vendor := vendorFor(t, codexVendor)
	compat := ai.CompatOf[ai.OpenAIResponsesCompat](vendor.Model("-"))
	compat.CodexClientVersion = "0.169.1"
	vendor.Compat = compat
	p := newVendorProvider("openai:subscription", vendor, endpoint.Config{
		BaseURL:    server.URL,
		Models:     []ai.Model{}, // Isolate the live catalog fixture from the bundled lineup.
		HTTPClient: server.Client(),
		Headers:    map[string]string{"Authorization": "Bearer test-token"},
	})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("catalog returned %d models, want one visible model: %+v", len(models), models)
	}
	m := models[0]
	if m.ID != "new-codex-model" || m.Name != "New Codex Model" || m.ContextWindow != 123456 || m.Description != "Fast coding and reasoning." {
		t.Fatalf("live model metadata lost: %+v", m)
	}
	if m.Reasoning == nil || m.Reasoning.DefaultEffort != "high" ||
		!slices.Equal(m.Reasoning.SupportedEfforts, []string{"low", "high", "ultra"}) {
		t.Fatalf("live reasoning metadata lost: %+v", m.Reasoning)
	}
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CacheModels(OpenAI, AuthSubscription, models); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	cached, ok := reloaded.CachedModels(OpenAI, AuthSubscription)
	if !ok || len(cached) != 1 || cached[0].Description != m.Description {
		t.Fatalf("cached description lost: %+v", cached)
	}
}
