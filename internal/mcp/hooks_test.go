package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopeConfigSource(t *testing.T) {
	tests := []struct {
		scope Scope
		want  string
	}{
		{ScopeUser, "user_settings"},
		{ScopeProject, "project_settings"},
		{ScopeLocal, "local_settings"},
	}
	for _, tt := range tests {
		if got := scopeConfigSource(tt.scope); got != tt.want {
			t.Fatalf("scopeConfigSource(%q) = %q, want %q", tt.scope, got, tt.want)
		}
	}
}

func TestConfigSourceFromFilePath(t *testing.T) {
	if got := configSourceFromFilePath("/repo/.san/mcp.local.json"); got != "local_settings" {
		t.Fatalf("unexpected local source: %q", got)
	}
	if got := configSourceFromFilePath("/repo/.san/mcp.json"); got != "project_settings" {
		t.Fatalf("unexpected project source: %q", got)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	userPath := filepath.Join(homeDir, ".san", "mcp.json")
	if got := configSourceFromFilePath(userPath); got != "user_settings" {
		t.Fatalf("unexpected user source: %q", got)
	}
}
