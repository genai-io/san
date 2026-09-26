package mcp

import (
	"maps"
	"testing"
	"time"
)

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
	waitFor(t, "the dropped server's transport to close", tr.isClosed)
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
	waitFor(t, "the stale server's transport to close", tr.isClosed)
}

// waitFor polls cond, since the teardown of servers that did not survive runs
// detached — Disconnect can block for seconds and Initialize is on the UI
// goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("timed out waiting for %s", what)
}
