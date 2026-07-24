package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/mcp/transport"
)

// slowTransport takes as long to close as a wedged stdio server: Close waits on
// the read loop (2s) and then the child's exit (5s).
type slowTransport struct {
	closeDelay time.Duration
	closed     chan struct{}
}

func newSlowTransport(d time.Duration) *slowTransport {
	return &slowTransport{closeDelay: d, closed: make(chan struct{})}
}

func (t *slowTransport) Start(context.Context) error { return nil }
func (t *slowTransport) Send(context.Context, *transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	return &transport.JSONRPCResponse{}, nil
}
func (t *slowTransport) SendNotification(context.Context, *transport.JSONRPCNotification) error {
	return nil
}
func (t *slowTransport) Close() error {
	time.Sleep(t.closeDelay)
	close(t.closed)
	return nil
}
func (t *slowTransport) IsAlive() bool                                        { return true }
func (t *slowTransport) SetNotificationHandler(transport.NotificationHandler) {}

// Local fixtures: deliberately not shared with other test files in this
// package, so this change stays independent of any other in-flight one.
func connectedRegistry(t *testing.T, servers map[string]transport.Transport) *Registry {
	t.Helper()
	r := newEmptyRegistry()
	for name, tr := range servers {
		cfg := ServerConfig{Name: name, Type: "stdio", Command: name}
		c := NewClient(cfg)
		c.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		c.mu.Lock()
		c.transport = tr
		c.connected = true
		c.mu.Unlock()
		r.configs[name] = cfg
		r.clients[name] = c
		r.retainWithoutLeases[name] = true
	}
	return r
}

// Disconnect used to call Client.Disconnect while holding the registry write
// lock, from the bubbletea Update goroutine. For up to seven seconds per
// server the UI processed no keys and repainted nothing, and the agent
// goroutine blocked with it — CallTool takes the read lock.
func TestDisconnectDoesNotBlockOnATeardown(t *testing.T) {
	tr := newSlowTransport(500 * time.Millisecond)
	r := connectedRegistry(t, map[string]transport.Transport{"wedged": tr})

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
	case <-tr.closed:
	case <-time.After(2 * time.Second):
		t.Error("the transport was never torn down")
	}
}

// DisconnectAll had the same shape, serialized across every server under one
// deferred lock.
func TestDisconnectAllDoesNotBlockPerServer(t *testing.T) {
	servers := map[string]transport.Transport{}
	transports := make([]*slowTransport, 0, 3)
	for _, name := range []string{"a", "b", "c"} {
		tr := newSlowTransport(300 * time.Millisecond)
		transports = append(transports, tr)
		servers[name] = tr
	}
	r := connectedRegistry(t, servers)

	start := time.Now()
	r.DisconnectAll()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("DisconnectAll blocked for %v", elapsed)
	}

	for i, tr := range transports {
		select {
		case <-tr.closed:
		case <-time.After(2 * time.Second):
			t.Errorf("transport %d was never torn down", i)
		}
	}
}

func TestDisconnectAllNotifiesToolsChanged(t *testing.T) {
	tr := newSlowTransport(0)
	r := connectedRegistry(t, map[string]transport.Transport{"shared": tr})
	notified := make(chan struct{}, 1)
	r.SetOnToolsChanged(func() { notified <- struct{}{} })

	r.DisconnectAll()
	select {
	case <-notified:
	case <-time.After(time.Second):
		t.Fatal("DisconnectAll did not notify tool-schema subscribers")
	}
}

func TestToolsChangedCallbackCanReenterConnectionLifecycle(t *testing.T) {
	tr := newSlowTransport(0)
	r := connectedRegistry(t, map[string]transport.Transport{"shared": tr})
	done := make(chan struct{})
	r.SetOnToolsChanged(func() {
		r.DisconnectAll()
		close(done)
	})

	r.Disconnect("shared")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tools-changed callback deadlocked reentering the lifecycle API")
	}
}

// Disconnecting a server that is not connected is a no-op, not a panic.
func TestDisconnectUnknownServerIsANoop(t *testing.T) {
	r := connectedRegistry(t, nil)
	r.Disconnect("never-connected")
}
