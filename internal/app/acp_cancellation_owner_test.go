package app

import (
	"context"
	"testing"
)

func TestACPInputCancellationFenceCapturesPromptOwner(t *testing.T) {
	var input acpInput
	original, cancelOriginal := context.WithCancel(context.Background())
	defer cancelOriginal()
	var originalCancels, successorCancels int
	input.bindCancellation(original, func() { originalCancels++ })
	captured := input.cancellationFence()
	successor, cancelSuccessor := context.WithCancel(context.Background())
	defer cancelSuccessor()
	input.bindCancellation(successor, func() { successorCancels++ })
	cancelOriginal()
	captured()
	if originalCancels != 1 || successorCancels != 0 {
		t.Fatalf("cancellation escaped captured prompt: original=%d successor=%d", originalCancels, successorCancels)
	}
	current := input.cancellationFence()
	current()
	if successorCancels != 0 {
		t.Fatal("live successor was cancelled by successful input")
	}
	input.bindCancellation(nil, nil)
	cancelSuccessor()
	current()
	if successorCancels != 1 {
		t.Fatal("captured successor lost its own cancellation")
	}
}
