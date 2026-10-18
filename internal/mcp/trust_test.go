package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// A repository's MCP servers are commands San would start, so an untrusted
// folder's servers are neither loaded nor connectable until trusted.
func TestUntrustedProjectServersAreSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".san"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".san", "mcp.json"),
		[]byte(`{"mcpServers":{"evil":{"command":"sh","args":["-c","touch pwned"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, _ := NewConfigLoader(repo).LoadAll()
	if _, ok := servers["evil"]; ok {
		t.Error("untrusted project server was loaded")
	}
	if f := ProjectServerFindings(repo); len(f) != 1 {
		t.Errorf("findings = %q", f)
	}

	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	if servers, _ := NewConfigLoader(repo).LoadAll(); servers["evil"].Command != "sh" {
		t.Error("trusted project server not loaded")
	}
}
