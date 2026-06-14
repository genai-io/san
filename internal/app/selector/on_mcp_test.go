package selector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coremcp "github.com/genai-io/san/internal/mcp"
)

func withTestRegistry(t *testing.T, reg *coremcp.Registry) {
	t.Helper()
	coremcp.SetDefaultRegistry(reg)
	t.Cleanup(coremcp.ResetDefaultRegistry)
}

func TestHandleCommand_UninitializedRegistryMessage(t *testing.T) {
	withTestRegistry(t, nil)
	selector := NewMCP(nil)

	result, editInfo, err := HandleMCPCommand(context.Background(), &selector, 80, 24, "")
	if err != nil {
		t.Fatalf("HandleMCPCommand() error = %v", err)
	}
	if editInfo != nil {
		t.Fatalf("expected nil edit info, got %#v", editInfo)
	}
	if !strings.Contains(result, "MCP is not initialized") {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestHandleCommand_EmptyArgsOpensSelector(t *testing.T) {
	reg := coremcp.NewRegistryForTest(map[string]coremcp.ServerConfig{
		"demo": {Name: "demo", Command: "echo", Scope: coremcp.ScopeLocal},
	})
	withTestRegistry(t, reg)
	selector := NewMCP(reg)

	result, editInfo, err := HandleMCPCommand(context.Background(), &selector, 80, 24, "")
	if err != nil {
		t.Fatalf("HandleMCPCommand() error = %v", err)
	}
	if result != "" || editInfo != nil {
		t.Fatalf("unexpected outputs: result=%q editInfo=%#v", result, editInfo)
	}
	if !selector.active {
		t.Fatal("expected selector to become active")
	}
}

func TestPrepareServerEditAndApplyServerEdit_RoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpHome)

	reg, err := coremcp.NewRegistry(tmpDir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	withTestRegistry(t, reg)

	err = reg.AddServer("demo", coremcp.ServerConfig{
		Type:    coremcp.TransportHTTP,
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	}, coremcp.ScopeProject)
	if err != nil {
		t.Fatalf("AddServer() error = %v", err)
	}

	info, err := coremcp.PrepareServerEdit(reg, "demo")
	if err != nil {
		t.Fatalf("PrepareServerEdit() error = %v", err)
	}
	if info.Scope != coremcp.ScopeProject {
		t.Fatalf("expected project scope, got %q", info.Scope)
	}

	data, err := os.ReadFile(info.TempFile)
	if err != nil {
		t.Fatalf("ReadFile(temp) error = %v", err)
	}
	if strings.Contains(string(data), `"name"`) || strings.Contains(string(data), `"scope"`) {
		t.Fatalf("temp edit file should not contain metadata fields: %s", data)
	}

	edited := `{"type":"http","url":"https://example.com/v2","headers":{"Authorization":"Bearer rotated-token"}}`
	if err := os.WriteFile(info.TempFile, []byte(edited), 0o644); err != nil {
		t.Fatalf("WriteFile(temp) error = %v", err)
	}

	if err := coremcp.ApplyServerEdit(reg, info); err != nil {
		t.Fatalf("ApplyServerEdit() error = %v", err)
	}

	cfg, ok := reg.GetConfig("demo")
	if !ok {
		t.Fatal("expected edited server config to exist")
	}
	if cfg.URL != "https://example.com/v2" {
		t.Fatalf("URL = %q, want %q", cfg.URL, "https://example.com/v2")
	}
	if cfg.Scope != coremcp.ScopeProject {
		t.Fatalf("scope = %q, want %q", cfg.Scope, coremcp.ScopeProject)
	}
	if _, err := os.Stat(info.TempFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp edit file to be removed, stat err = %v", err)
	}
}

func TestHandleGet_MasksSecretsAndShowsDefaults(t *testing.T) {
	reg := coremcp.NewRegistryForTest(map[string]coremcp.ServerConfig{
		"api": {
			Name:    "api",
			Type:    coremcp.TransportHTTP,
			URL:     "https://example.com/mcp",
			Env:     map[string]string{"API_KEY": "super-secret"},
			Headers: map[string]string{"Authorization": "Bearer secret-token"},
		},
	})
	withTestRegistry(t, reg)

	result, err := handleMCPGet(reg, "api")
	if err != nil {
		t.Fatalf("handleMCPGet() error = %v", err)
	}
	if !strings.Contains(result, "Scope:  local") {
		t.Fatalf("expected default local scope, got %q", result)
	}
	if !strings.Contains(result, "API_KEY=***") {
		t.Fatalf("expected masked env value, got %q", result)
	}
	if !strings.Contains(result, "Authorization: ***") {
		t.Fatalf("expected masked header value, got %q", result)
	}
}

func Test_parseScopeAndKeyValues(t *testing.T) {
	if coremcp.ParseScope("global") != coremcp.ScopeUser {
		t.Fatal("expected global alias to map to user scope")
	}
	if coremcp.ParseScope("project") != coremcp.ScopeProject {
		t.Fatal("expected project scope")
	}
	if coremcp.ParseScope("anything-else") != coremcp.ScopeLocal {
		t.Fatal("expected unknown scope to default to local")
	}

	got := coremcp.ParseKeyValues([]string{"API_KEY = secret ", " BadEntry ", "X-Test: abc "}, "=")
	if len(got) != 1 || got["API_KEY"] != "secret" {
		t.Fatalf("unexpected parsed equals map: %#v", got)
	}

	got = coremcp.ParseKeyValues([]string{"Authorization: Bearer token", "InvalidHeader"}, ":")
	if len(got) != 1 || got["Authorization"] != "Bearer token" {
		t.Fatalf("unexpected parsed header map: %#v", got)
	}
}

func TestApplyServerEdit_InvalidJSONReturnsErrorAndRemovesTempFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(tmpFile, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := coremcp.ApplyServerEdit(nil, &coremcp.EditInfo{
		TempFile:   tmpFile,
		ServerName: "demo",
		Scope:      coremcp.ScopeLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
	if _, statErr := os.Stat(tmpFile); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp file removal after failure, stat err = %v", statErr)
	}
}
