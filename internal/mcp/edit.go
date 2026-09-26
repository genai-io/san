package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// EditInfo carries the temp file, server name and scope for editing a single server config.
type EditInfo struct {
	TempFile   string
	ServerName string
	Scope      Scope
}

// PrepareServerEdit extracts a single server's config into a temp file for editing.
// The caller is responsible for calling ApplyServerEdit after the editor closes
// and removing the temp file.
func PrepareServerEdit(reg *Manager, name string) (*EditInfo, error) {
	config, ok := reg.Config(name)
	if !ok {
		return nil, fmt.Errorf("server not found: %s\n\nUse /mcp list to see available servers", name)
	}

	scope := config.Scope
	if scope == "" {
		scope = ScopeLocal
	}

	// Strip metadata fields before serializing
	configToEdit := config
	configToEdit.Name = ""
	configToEdit.Scope = ""

	data, err := json.MarshalIndent(configToEdit, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize config: %w", err)
	}

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("mcp-%s-*.json", name))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, writeErr := tmpFile.Write(append(data, '\n')); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return nil, fmt.Errorf("failed to write temp file: %w", writeErr)
	}
	_ = tmpFile.Close()

	return &EditInfo{TempFile: tmpFile.Name(), ServerName: name, Scope: scope}, nil
}

// ApplyServerEdit reads the edited temp file and saves the updated config back.
func ApplyServerEdit(reg *Manager, info *EditInfo) error {
	defer func() { _ = os.Remove(info.TempFile) }()

	data, err := os.ReadFile(info.TempFile)
	if err != nil {
		return fmt.Errorf("failed to read edited config: %w", err)
	}

	var updated ServerConfig
	if err := json.Unmarshal(data, &updated); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	if err := reg.AddServer(info.ServerName, updated, info.Scope); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// ParseScope converts a string scope name to the MCP scope constant.
func ParseScope(s string) Scope {
	switch strings.ToLower(s) {
	case "user", "global":
		return ScopeUser
	case "project":
		return ScopeProject
	default:
		return ScopeLocal
	}
}

// ParseKeyValues parses key=value or key:value pairs.
func ParseKeyValues(items []string, sep string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		if key, value, ok := strings.Cut(item, sep); ok {
			result[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return result
}
