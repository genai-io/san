package mcp

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLeaseReleaseDoesNotDisconnectPreexistingRetainedConnection(t *testing.T) {
	tr := newSlowSession(0)
	registry := connectedRegistry(t, map[string]*fakeSession{"shared": tr})

	cleanup, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("AcquireServerConnectionLeases() errors = %v", errs)
	}
	cleanup()

	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("cleanup disconnected a connection owned by another caller")
	}
}

func TestConcurrentLeaseAcquisitionSharesOneConnectionUntilFinalRelease(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := newFakeSession()
	var factoryCalls int
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		factoryCalls++
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	start := make(chan struct{})
	cleanups := make(chan func(), 2)
	errors := make(chan []error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			cleanup, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
			cleanups <- cleanup
			errors <- errs
		})
	}
	close(start)
	wg.Wait()

	for range 2 {
		if errs := <-errors; len(errs) != 0 {
			t.Fatalf("AcquireServerConnectionLeases() errors = %v", errs)
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

func TestExplicitConnectRetainsLeaseCreatedConnectionAfterFinalRelease(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := newFakeSession()
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	cleanup, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("AcquireServerConnectionLeases() errors = %v", errs)
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

func TestLeaseSetReleaseIsIdempotent(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	tr := newFakeSession()
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	first, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("first AcquireServerConnectionLeases() errors = %v", errs)
	}
	second, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("second AcquireServerConnectionLeases() errors = %v", errs)
	}

	first()
	first()
	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("repeated cleanup released another caller's lease")
	}
	second()
	waitFor(t, "the final lease cleanup to close the transport", func() bool { return tr.closeCount() == 1 })
}

func TestDeadConnectionReplacementKeepsLeaseCountsSeparatedByConnectionEpoch(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	firstSession := newFakeSession()
	secondSession := newFakeSession()
	sessions := []*fakeSession{firstSession, secondSession}
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		tr := sessions[0]
		sessions = sessions[1:]
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	first, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("first AcquireServerConnectionLeases() errors = %v", errs)
	}
	firstSession.mu.Lock()
	firstSession.closed = true
	firstSession.mu.Unlock()
	second, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("replacement AcquireServerConnectionLeases() errors = %v", errs)
	}

	first()
	second()
	if _, ok := registry.GetClient("shared"); ok {
		t.Fatal("generation-specific lease cleanup leaked the replacement connection")
	}
	waitFor(t, "the replacement session to close", func() bool { return secondSession.closeCount() == 1 })
}

func TestExplicitRetentionIntentAppliesToLeaseTriggeredReplacement(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	firstSession := newFakeSession()
	secondSession := newFakeSession()
	sessions := []*fakeSession{firstSession, secondSession}
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		tr := sessions[0]
		sessions = sessions[1:]
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	if err := registry.Connect(context.Background(), "shared"); err != nil {
		t.Fatalf("persistent Connect() error = %v", err)
	}
	firstSession.mu.Lock()
	firstSession.closed = true
	firstSession.mu.Unlock()
	cleanup, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("AcquireServerConnectionLeases() errors = %v", errs)
	}
	cleanup()

	if _, ok := registry.GetClient("shared"); !ok {
		t.Fatal("lease cleanup disconnected a replacement with persistent ownership")
	}
	if secondSession.closeCount() != 0 {
		t.Fatal("lease cleanup closed a persistently owned replacement")
	}
}

func TestOldEpochLeaseReleaseCannotDisconnectCurrentConnection(t *testing.T) {
	registry := NewRegistryForTest(map[string]ServerConfig{
		"shared": {Name: "shared", Type: TransportSTDIO, Command: "shared"},
	})
	first := newFakeSession()
	second := newFakeSession()
	sessions := []*fakeSession{first, second}
	registry.newClientForConfig = func(cfg ServerConfig) *Client {
		tr := sessions[0]
		sessions = sessions[1:]
		client := NewClient(cfg)
		client.dial = dialing(tr)
		return client
	}

	cleanup, errs := AcquireServerConnectionLeases(context.Background(), registry, []string{"shared"})
	if len(errs) != 0 {
		t.Fatalf("AcquireServerConnectionLeases() errors = %v", errs)
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
	if !second.Alive() {
		t.Fatal("stale lease cleanup closed the replacement transport")
	}
}
