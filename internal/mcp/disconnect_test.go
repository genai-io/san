package mcp

import (
	"testing"
	"time"
)

// slowTransport takes as long to close as a wedged stdio server: Close waits on
// the read loop (2s) and then the child's exit (5s).
// connectedRegistry is a registry holding already-connected clients, each
// reaching a fake session rather than a server.
func connectedRegistry(t *testing.T, servers map[string]*fakeSession) *Registry {
	t.Helper()
	r := newEmptyRegistry()
	for name, session := range servers {
		cfg := ServerConfig{Name: name, Type: "stdio", Command: name}
		r.configs[name] = cfg
		r.clients[name] = connectedClient(t, cfg, session)
		r.getOrCreateConnectionState(name).retainWithoutLeases = true
	}
	return r
}

// Disconnect used to call Client.Disconnect while holding the registry write
// lock, from the bubbletea Update goroutine. For up to seven seconds per
// server the UI processed no keys and repainted nothing, and the agent
// goroutine blocked with it — CallTool takes the read lock.
func TestDisconnectDoesNotBlockOnATeardown(t *testing.T) {
	tr := newSlowSession(500 * time.Millisecond)
	r := connectedRegistry(t, map[string]*fakeSession{"wedged": tr})

	start := time.Now()
	r.Disconnect("wedged")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("Disconnect blocked for %v; the UI would be frozen for that long", elapsed)
	}
	// The server is gone from the registry immediately, teardown or not.
	if _, ok := r.GetClient("wedged"); ok {
		t.Error("the server is still in the registry after Disconnect returned")
	}
	// And the lock is free right away, which is what the agent needs.
	done := make(chan struct{})
	go func() { r.GetToolSchemas(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("the registry lock was still held; the agent goroutine would block too")
	}

	select {
	case <-tr.done():
	case <-time.After(2 * time.Second):
		t.Error("the session was never torn down")
	}
}

// DisconnectAll had the same shape, serialized across every server under one
// deferred lock.
func TestDisconnectAllDoesNotBlockPerServer(t *testing.T) {
	servers := map[string]*fakeSession{}
	sessions := make([]*fakeSession, 0, 3)
	for _, name := range []string{"a", "b", "c"} {
		tr := newSlowSession(300 * time.Millisecond)
		sessions = append(sessions, tr)
		servers[name] = tr
	}
	r := connectedRegistry(t, servers)

	start := time.Now()
	r.DisconnectAll()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("DisconnectAll blocked for %v", elapsed)
	}

	for i, tr := range sessions {
		select {
		case <-tr.done():
		case <-time.After(2 * time.Second):
			t.Errorf("transport %d was never torn down", i)
		}
	}
}

// Disconnecting a server that is not connected is a no-op, not a panic.
func TestDisconnectUnknownServerIsANoop(t *testing.T) {
	r := connectedRegistry(t, nil)
	r.Disconnect("never-connected")
}
