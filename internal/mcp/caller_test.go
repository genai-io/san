package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/mcp/transport"
)

func TestConnectServersLeavesExistingConnectionOwned(t *testing.T) {
	tr := newSlowTransport(0)
	registry := connectedRegistry(t, map[string]transport.Transport{"shared": tr})

	cleanup, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	cleanup()

	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("cleanup disconnected a connection owned by another caller")
	}
}

type leaseTransport struct {
	mu     sync.Mutex
	alive  bool
	closed int
}

func (t *leaseTransport) Start(context.Context) error {
	t.mu.Lock()
	t.alive = true
	t.mu.Unlock()
	return nil
}

func (t *leaseTransport) Send(_ context.Context, req *transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	result := json.RawMessage(`{}`)
	return &transport.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, nil
}

func (t *leaseTransport) SendNotification(context.Context, *transport.JSONRPCNotification) error {
	return nil
}

func (t *leaseTransport) Close() error {
	t.mu.Lock()
	t.alive = false
	t.closed++
	t.mu.Unlock()
	return nil
}

func (t *leaseTransport) IsAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.alive
}

func (t *leaseTransport) SetNotificationHandler(transport.NotificationHandler) {}

func (t *leaseTransport) closeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func TestConnectServersSharesConcurrentLeases(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := &leaseTransport{}
	var factoryCalls int
	registry.clientFactory = func(cfg ServerConfig) *Client {
		factoryCalls++
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	start := make(chan struct{})
	cleanups := make(chan func(), 2)
	errors := make(chan []error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cleanup, errs := ConnectServers(context.Background(), registry, []string{"shared"})
			cleanups <- cleanup
			errors <- errs
		}()
	}
	close(start)
	wg.Wait()

	for range 2 {
		if errs := <-errors; len(errs) != 0 {
			t.Fatalf("ConnectServers() errors = %v", errs)
		}
	}
	if factoryCalls != 1 {
		t.Fatalf("client factory calls = %d, want 1", factoryCalls)
	}

	firstCleanup := <-cleanups
	secondCleanup := <-cleanups
	firstCleanup()
	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("first cleanup disconnected a connection still leased by another caller")
	}
	if tr.closeCount() != 0 {
		t.Fatal("first cleanup closed the shared transport")
	}

	secondCleanup()
	if _, ok := registry.GetClient("shared"); ok {
		t.Fatal("last cleanup left its temporary connection in the registry")
	}
	deadline := time.Now().Add(time.Second)
	for tr.closeCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tr.closeCount() != 1 {
		t.Fatalf("transport close count = %d, want 1", tr.closeCount())
	}
}

func TestConnectPromotesLeasedConnectionToPersistent(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := &leaseTransport{}
	registry.clientFactory = func(cfg ServerConfig) *Client {
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	cleanup, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	if err := registry.Connect(context.Background(), "shared"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	cleanup()

	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("lease cleanup disconnected a connection promoted by an explicit Connect")
	}
	if tr.closeCount() != 0 {
		t.Fatal("lease cleanup closed a connection promoted by an explicit Connect")
	}
}

func TestConnectServersCleanupIsIdempotent(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := &leaseTransport{}
	registry.clientFactory = func(cfg ServerConfig) *Client {
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	first, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("first ConnectServers() errors = %v", errs)
	}
	second, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("second ConnectServers() errors = %v", errs)
	}

	first()
	first()
	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("repeated cleanup released another caller's lease")
	}
	second()
	waitFor(t, "the final lease cleanup to close the transport", func() bool { return tr.closeCount() == 1 })
}

func TestLeaseReconnectAfterTransportDeathTracksGenerations(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	firstTransport := &leaseTransport{}
	secondTransport := &leaseTransport{}
	transports := []transport.Transport{firstTransport, secondTransport}
	registry.clientFactory = func(cfg ServerConfig) *Client {
		tr := transports[0]
		transports = transports[1:]
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	first, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("first ConnectServers() errors = %v", errs)
	}
	firstTransport.mu.Lock()
	firstTransport.alive = false
	firstTransport.mu.Unlock()
	second, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("replacement ConnectServers() errors = %v", errs)
	}

	first()
	second()
	if _, ok := registry.GetClient("shared"); ok {
		t.Fatal("generation-specific lease cleanup leaked the replacement connection")
	}
	waitFor(t, "the replacement transport to close", func() bool { return secondTransport.closeCount() == 1 })
}

func TestPersistentIntentSurvivesLeaseReconnect(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	firstTransport := &leaseTransport{}
	secondTransport := &leaseTransport{}
	transports := []transport.Transport{firstTransport, secondTransport}
	registry.clientFactory = func(cfg ServerConfig) *Client {
		tr := transports[0]
		transports = transports[1:]
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	if err := registry.Connect(context.Background(), "shared"); err != nil {
		t.Fatalf("persistent Connect() error = %v", err)
	}
	firstTransport.mu.Lock()
	firstTransport.alive = false
	firstTransport.mu.Unlock()
	cleanup, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	cleanup()

	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("lease cleanup disconnected a replacement with persistent ownership")
	}
	if secondTransport.closeCount() != 0 {
		t.Fatal("lease cleanup closed a persistently owned replacement")
	}
}

func TestStaleLeaseCleanupDoesNotDisconnectReplacement(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	first := &leaseTransport{}
	second := &leaseTransport{}
	transports := []transport.Transport{first, second}
	registry.clientFactory = func(cfg ServerConfig) *Client {
		tr := transports[0]
		transports = transports[1:]
		client := NewClient(cfg)
		client.TransportFactory = func() (transport.Transport, error) { return tr, nil }
		return client
	}

	cleanup, errs := ConnectServers(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("ConnectServers() errors = %v", errs)
	}
	registry.Disconnect("shared")
	if err := registry.Connect(context.Background(), "shared"); err != nil {
		t.Fatalf("replacement Connect() error = %v", err)
	}
	cleanup()

	client, ok := registry.GetClient("shared")
	if !ok || client == nil {
		t.Fatal("stale lease cleanup removed the replacement connection")
	}
	if !second.IsAlive() {
		t.Fatal("stale lease cleanup closed the replacement transport")
	}
}
