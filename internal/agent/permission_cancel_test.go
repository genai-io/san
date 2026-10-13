package agent

import (
	"context"
	"testing"
	"time"
)

func TestPermissionGateCancellationWinsOverLateApproval(t *testing.T) {
	for _, closeGate := range []bool{false, true} {
		for range 100 {
			gate := NewPermissionGate(nil)
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan bool, 1)
			go func() { allow, _ := gate.prompt(ctx, &PermGateRequest{}); result <- allow }()
			req, ok := gate.Recv()
			if !ok {
				t.Fatal("prompt was not delivered")
			}
			if closeGate {
				gate.Close()
			} else {
				cancel()
			}
			req.Response <- PermGateResponse{Allow: true}
			select {
			case allow := <-result:
				if allow {
					t.Fatal("a canceled or closed gate accepted a late approval")
				}
			case <-time.After(time.Second):
				t.Fatal("gate remained blocked after cancellation")
			}
			cancel()
			gate.Close()
		}
	}
}
