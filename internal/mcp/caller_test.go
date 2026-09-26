package mcp

import (
	"context"
	"testing"
)

// A subagent listing a server the session already uses must not take that
// connection down when it finishes.
func TestConnectServersCleanupKeepsPreexistingConnection(t *testing.T) {
	r := connectedRegistry(t, map[string]*fakeSession{"shared": newFakeSession()})

	cleanup, errs := ConnectServers(context.Background(), r, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	cleanup()

	if _, ok := r.GetClient("shared"); !ok {
		t.Fatal("cleanup disconnected a connection the session already owned")
	}
}

func TestConnectServersCleanupDisconnectsWhatItConnected(t *testing.T) {
	r := NewRegistryForTest(map[string]ServerConfig{"own": {Name: "own", Type: TransportSTDIO, Command: "own"}})
	r.newClientForConfig = func(cfg ServerConfig) *Client {
		c := NewClient(cfg)
		c.dial = dialing(newFakeSession())
		return c
	}

	cleanup, errs := ConnectServers(context.Background(), r, []string{"own"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	if _, ok := r.GetClient("own"); !ok {
		t.Fatal("server not connected")
	}
	cleanup()

	if _, ok := r.GetClient("own"); ok {
		t.Fatal("cleanup left the subagent's own connection open")
	}
}
