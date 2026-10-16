package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai/endpoint"
)

func TestCodexCatalogKeepsLiveCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("catalog request = %s %s, want GET /models", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// A model absent from the built-in snapshot must still reach the picker
		// with the limits and reasoning ladder advertised by the live catalog.
		fmt.Fprint(w, `{"models":[
			{"slug":"new-codex-model","display_name":"New Codex Model","context_window":123456,
			 "default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"},{"effort":"ultra"}]},
			{"slug":"hidden-model","show_in_picker":false},
			{"slug":""}
		]}`)
	}))
	defer server.Close()

	p := newVendorProvider("openai:subscription", vendorFor(t, codexVendor), endpoint.Config{
		BaseURL:    server.URL,
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
	if m.ID != "new-codex-model" || m.Name != "New Codex Model" || m.ContextWindow != 123456 {
		t.Fatalf("live model metadata lost: %+v", m)
	}
	if m.Reasoning == nil || m.Reasoning.DefaultEffort != "high" ||
		!slices.Equal(m.Reasoning.SupportedEfforts, []string{"low", "high", "ultra"}) {
		t.Fatalf("live reasoning metadata lost: %+v", m.Reasoning)
	}
}
