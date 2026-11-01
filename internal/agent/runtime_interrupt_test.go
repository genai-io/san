package agent

import (
	"context"
	"testing"
)

func TestRuntimeCancelInactive(t *testing.T) {
	runtime := &Runtime{}
	receipt := runtime.CancelTurn()
	if receipt != nil {
		t.Fatal("inactive runtime captured a turn")
	}
	select {
	case <-receipt.ExecutionDone():
	default:
		t.Fatal("inactive execution completion is not closed")
	}
	if err := receipt.Wait(context.Background()); err == nil {
		t.Fatal("missing turn finalized successfully")
	}
	if runtime.Active() {
		t.Fatal("cancellation activated runtime")
	}
}
