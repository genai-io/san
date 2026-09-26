package mcp

import (
	"testing"
)

func TestRegistryConnectingState(t *testing.T) {
	configs := map[string]ServerConfig{
		"server1": {Name: "server1", URL: "http://example.com/mcp"},
		"server2": {Name: "server2", URL: "http://example2.com/mcp"},
	}
	reg := NewManagerForTest(configs)

	// Initially all disconnected
	for _, s := range reg.List() {
		if s.Status != StatusDisconnected {
			t.Errorf("expected disconnected, got %s for %s", s.Status, s.Config.Name)
		}
	}

	// Mark server1 as connecting
	reg.SetConnecting("server1", true)
	for _, s := range reg.List() {
		switch s.Config.Name {
		case "server1":
			if s.Status != StatusConnecting {
				t.Errorf("expected connecting, got %s", s.Status)
			}
		case "server2":
			if s.Status != StatusDisconnected {
				t.Errorf("expected disconnected, got %s", s.Status)
			}
		}
	}

	// Clear connecting and set error
	reg.SetConnecting("server1", false)
	reg.SetConnectError("server1", "connection refused")
	for _, s := range reg.List() {
		if s.Config.Name == "server1" {
			if s.Status != StatusError {
				t.Errorf("expected error, got %s", s.Status)
			}
			if s.Error != "connection refused" {
				t.Errorf("expected error msg, got %q", s.Error)
			}
		}
	}

	// Disabled server should stay disconnected even if not connecting
	reg.SetDisabled("server2", true)
	for _, s := range reg.List() {
		if s.Config.Name == "server2" {
			if s.Status != StatusDisconnected {
				t.Errorf("disabled server should be disconnected, got %s", s.Status)
			}
		}
	}

	// Clear error
	reg.SetConnectError("server1", "")
	for _, s := range reg.List() {
		if s.Config.Name == "server1" {
			if s.Status != StatusDisconnected {
				t.Errorf("expected disconnected after clearing error, got %s", s.Status)
			}
		}
	}

	t.Log("All connecting state transitions verified OK")
}
