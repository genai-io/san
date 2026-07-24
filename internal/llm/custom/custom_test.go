package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/llm"
)

func newTestStore(t *testing.T) *llm.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := llm.NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

// modelsServer serves an OpenAI-compatible GET /models endpoint returning the
// given model IDs.
func modelsServer(t *testing.T, ids ...string) *httptest.Server {
	t.Helper()
	var data strings.Builder
	for i, id := range ids {
		if i > 0 {
			data.WriteString(",")
		}
		data.WriteString(`{"id":"` + id + `","object":"model"}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[` + data.String() + `]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRegisteredUnderFixedID(t *testing.T) {
	if !llm.IsProvider(llm.Name(DefaultID)) {
		t.Fatal("custom provider should be registered under the fixed ID at init")
	}
	if llm.ProviderDisplayName(llm.Name(DefaultID)) != "Custom" {
		t.Fatalf("display name = %q, want Custom", llm.ProviderDisplayName(llm.Name(DefaultID)))
	}
}

func TestNewClientFailsWithoutConfig(t *testing.T) {
	newTestStore(t) // empty store: no custom provider config

	_, err := llm.GetProvider(context.Background(), llm.Name(DefaultID), llm.AuthAPIKey)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestListModelsViaEndpoint(t *testing.T) {
	store := newTestStore(t)
	srv := modelsServer(t, "model-a", "model-b")
	if err := store.SetCustomProvider(llm.CustomProviderConfig{ID: DefaultID, BaseURL: srv.URL}); err != nil {
		t.Fatalf("SetCustomProvider() error = %v", err)
	}
	t.Setenv(APIKeyEnvVar, "test-key")

	p, err := llm.GetProvider(context.Background(), llm.Name(DefaultID), llm.AuthAPIKey)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if p.Name() != DefaultID+":"+string(llm.AuthAPIKey) {
		t.Fatalf("Name() = %q", p.Name())
	}

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models) != 2 || models[0].ID != "model-a" || models[1].ID != "model-b" {
		t.Fatalf("unexpected models: %+v", models)
	}
}

func TestListModelsParsesContextLength(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"with-ctx","object":"model","context_length":131072},
			{"id":"with-max","object":"model","max_context_length":65536},
			{"id":"no-ctx","object":"model"}
		]}`))
	}))
	t.Cleanup(srv.Close)
	if err := store.SetCustomProvider(llm.CustomProviderConfig{ID: DefaultID, BaseURL: srv.URL}); err != nil {
		t.Fatalf("SetCustomProvider() error = %v", err)
	}
	t.Setenv(APIKeyEnvVar, "test-key")

	p, err := llm.GetProvider(context.Background(), llm.Name(DefaultID), llm.AuthAPIKey)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	want := map[string]int{"with-ctx": 131072, "with-max": 65536, "no-ctx": 0}
	got := make(map[string]int, len(models))
	for _, m := range models {
		got[m.ID] = m.InputTokenLimit
	}
	for id, limit := range want {
		if got[id] != limit {
			t.Fatalf("InputTokenLimit for %s = %d, want %d", id, got[id], limit)
		}
	}
}

func TestListModelsEmptyEndpointFails(t *testing.T) {
	store := newTestStore(t)
	srv := modelsServer(t)
	if err := store.SetCustomProvider(llm.CustomProviderConfig{ID: DefaultID, BaseURL: srv.URL}); err != nil {
		t.Fatalf("SetCustomProvider() error = %v", err)
	}
	t.Setenv(APIKeyEnvVar, "test-key")

	p, err := llm.GetProvider(context.Background(), llm.Name(DefaultID), llm.AuthAPIKey)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if _, err := p.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "no models") {
		t.Fatalf("expected no-models error, got %v", err)
	}
}
