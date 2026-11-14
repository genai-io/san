package mcp

import (
	"context"
	"maps"
	"strings"
	"testing"
	"time"
)

func TestAdoptLiveClientsRebindsNotifications(t *testing.T) {
	cfg := ServerConfig{Name: "docs"}
	client := connectedClient(t, cfg, newFakeSession())
	t.Cleanup(func() { _ = client.Disconnect() })
	old := registryWith(map[string]ServerConfig{"docs": cfg}, map[string]*Client{"docs": client})
	oldCalls, freshCalls := 0, 0
	old.SetOnToolsChanged(func() { oldCalls++ })
	client.SetOnToolsChanged(old.notifyToolsChanged)
	fresh := registryWith(map[string]ServerConfig{"docs": cfg}, nil)
	fresh.SetOnToolsChanged(func() { freshCalls++; fresh.mu.RLock(); fresh.mu.RUnlock() })
	fresh.adoptLiveClients(old)
	client.notifyToolsChanged()
	old.notifyToolsChanged()
	if oldCalls != 0 || freshCalls != 1 {
		t.Fatalf("notifications: old=%d fresh=%d; want 0, 1", oldCalls, freshCalls)
	}
	// Reusing the retired source must not steal the already transferred client.
	other := registryWith(map[string]ServerConfig{"docs": cfg}, nil)
	other.adoptLiveClients(old)
	if fresh.clients["docs"] != client || len(other.clients) != 0 {
		t.Fatal("repeated adoption changed ownership")
	}
	if err := old.Connect(context.Background(), "docs"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("Connect on retired manager: %v", err)
	}
}

func TestAdoptLiveClientsRejectsLateDial(t *testing.T) {
	cfg := ServerConfig{Name: "docs"}
	old := registryWith(map[string]ServerConfig{"docs": cfg}, nil)
	started, release := make(chan struct{}), make(chan struct{})
	tr := newFakeSession()
	t.Cleanup(func() { _ = tr.Close() })
	old.newClientForConfig = func(cfg ServerConfig) *Client {
		client := NewClient(cfg)
		client.dial = func(context.Context, func()) (conn, error) {
			close(started)
			<-release
			return tr, nil
		}
		return client
	}
	result := make(chan error, 1)
	go func() { result <- old.Connect(context.Background(), "docs") }()
	<-started
	fresh := registryWith(map[string]ServerConfig{"docs": cfg}, nil)
	fresh.adoptLiveClients(old)
	close(release)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "retired") {
		t.Errorf("late Connect: %v; want retirement error", err)
	}
	if len(old.clients) != 0 || len(fresh.clients) != 0 {
		t.Fatal("late dial retained by a manager")
	}
	select {
	case <-tr.done():
	case <-time.After(2 * time.Second):
		t.Fatal("late dial transport was not closed")
	}
}

func registryWith(configs map[string]ServerConfig, clients map[string]*Client) *Manager {
	r := newEmptyManager()
	maps.Copy(r.configs, configs)
	for name, c := range clients {
		r.clients[name] = c
	}
	return r
}

// A user-level MCP server is configured in both the old project and the new
// one. Replacing the registry on a cwd change used to drop its connection, so
// every mcp__* tool disappeared from the agent for the rest of the session with
// nothing to reconnect it — AutoConnect only runs at startup.
func TestAdoptLiveClientsKeepsUnchangedServersConnected(t *testing.T) {
	cfg := ServerConfig{Name: "docs", Type: "stdio", Command: "docs-server", Args: []string{"--stdio"}}
	tr := newFakeSession()
	old := registryWith(
		map[string]ServerConfig{"docs": cfg},
		map[string]*Client{"docs": connectedClient(t, cfg, tr)},
	)

	// Same server still configured after the cwd change.
	fresh := registryWith(map[string]ServerConfig{"docs": cfg}, nil)
	fresh.adoptLiveClients(old)

	if _, ok := fresh.clients["docs"]; !ok {
		t.Fatal("the live connection was not carried across; mcp__docs__* tools would vanish")
	}
	if tr.isClosed() {
		t.Error("an unchanged server's transport was closed")
	}
	if len(old.clients) != 0 {
		t.Error("the outgoing registry still owns the client; both would think they own it")
	}
}

// A project-scoped server that the new project does not configure must be torn
// down — it was previously dropped still running, leaking the subprocess.
func TestAdoptLiveClientsDisconnectsServersTheNewProjectDropped(t *testing.T) {
	cfg := ServerConfig{Name: "old-proj", Type: "stdio", Command: "old-server"}
	tr := newFakeSession()
	old := registryWith(
		map[string]ServerConfig{"old-proj": cfg},
		map[string]*Client{"old-proj": connectedClient(t, cfg, tr)},
	)

	fresh := registryWith(map[string]ServerConfig{}, nil) // new project configures nothing
	fresh.adoptLiveClients(old)

	if _, ok := fresh.clients["old-proj"]; ok {
		t.Error("a server the new project does not configure was adopted")
	}
	waitForSessionClose(t, tr)
}

// Same name, different command: the config changed, so the old process is not
// the server the new project asked for.
func TestAdoptLiveClientsRejectsAChangedConfig(t *testing.T) {
	oldCfg := ServerConfig{Name: "docs", Type: "stdio", Command: "docs-server", Args: []string{"--v1"}}
	newCfg := ServerConfig{Name: "docs", Type: "stdio", Command: "docs-server", Args: []string{"--v2"}}
	tr := newFakeSession()
	old := registryWith(
		map[string]ServerConfig{"docs": oldCfg},
		map[string]*Client{"docs": connectedClient(t, oldCfg, tr)},
	)

	fresh := registryWith(map[string]ServerConfig{"docs": newCfg}, nil)
	fresh.adoptLiveClients(old)

	if _, ok := fresh.clients["docs"]; ok {
		t.Error("adopted a connection whose configuration no longer matches")
	}
	waitForSessionClose(t, tr)
}

func waitForSessionClose(t *testing.T, session *fakeSession) {
	t.Helper()
	select {
	case <-session.done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transport to close")
	}
}
