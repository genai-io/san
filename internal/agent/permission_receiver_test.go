package agent

import (
	"context"
	"testing"
	"time"
)

func TestPermissionReceiverLifetime(t *testing.T) {
	gate := NewPermissionGate(nil)
	defer gate.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { _, ok := gate.WaitRequest(ctx); done <- ok }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("cancelled receiver returned request")
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not join")
	}
	next, stop := context.WithCancel(context.Background())
	defer stop()
	request := &PermGateRequest{Context: next}
	gate.requests <- request
	if got, ok := gate.WaitRequest(next); !ok || got != request {
		t.Fatal("turn cancellation closed session gate")
	}
}
