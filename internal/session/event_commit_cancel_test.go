package session

import (
	"context"
	"reflect"
	"testing"
)

// Cancellation lands immediately after San's admission check, before SDK entry.
type cancelAtCommitAdmission struct {
	context.Context
	cancel context.CancelFunc
}

func (c *cancelAtCommitAdmission) Err() error {
	err := c.Context.Err()
	c.cancel()
	return err
}

func TestEventAdmittedCommitRetainsStorageFailureAfterCancellation(t *testing.T) {
	_, writer, _ := eventStorageFixture(t)
	before := eventBytes(t, writer)
	prepared, err := writer.prepareEventBatch("failed", []Event{testEvent(2, "note", AnnotationAddPayload{Text: "must not appear"})}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.log.close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, failure := writer.commitEventBatch(&cancelAtCommitAdmission{Context: ctx, cancel: cancel}, prepared)
	if failure == nil || failure == context.Canceled || ctx.Err() != context.Canceled {
		t.Fatalf("storage failure was replaced by cancellation: %v", failure)
	}
	if writer.Err() != failure {
		t.Fatal("storage failure did not remain latched")
	}
	if _, err := writer.commitEventBatch(t.Context(), prepared); err != failure {
		t.Fatalf("failed writer accepted a later commit: %v", err)
	}
	if !reflect.DeepEqual(before, eventBytes(t, writer)) {
		t.Fatal("failed commit changed persisted facts")
	}
}

func TestEventAdmittedCommitSurvivesCallerCancellation(t *testing.T) {
	store, writer, _ := eventStorageFixture(t)
	before, err := writer.log.tail()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := writer.prepareEventBatch("admitted", []Event{testEvent(2, "note", AnnotationAddPayload{Text: "committed once"})}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	receipt, err := writer.commitEventBatch(&cancelAtCommitAdmission{Context: ctx, cancel: cancel}, prepared)
	if err != nil {
		t.Fatalf("admitted commit lost its receipt to cancellation: %v", err)
	}
	if ctx.Err() != context.Canceled || receipt.Tail == before {
		t.Fatal("fixture did not cancel the caller across the commit boundary")
	}
	if err := writer.Err(); err != nil {
		t.Fatalf("admitted cancellation poisoned writer: %v", err)
	}
	committed, err := writer.eventSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Events()) != 2 {
		t.Fatal("admitted batch was lost or duplicated")
	}
	replayed, tail, err := store.replayEvents(t.Context(), writer.ID(), nil)
	if err != nil || tail != receipt.Tail || !reflect.DeepEqual(replayed.Events(), committed.Events()) {
		t.Fatalf("disk, receipt and projection diverged: tail=%+v receipt=%+v err=%v", tail, receipt.Tail, err)
	}
	// An explicit identical retry must observe the same batch, not append it twice.
	retry, err := writer.commitEventBatch(t.Context(), prepared)
	if err != nil || retry.Tail != receipt.Tail {
		t.Fatalf("admitted batch could not be retried idempotently: %v", err)
	}
	later, err := writer.prepareEventBatch("later", []Event{testEvent(3, "later", AnnotationAddPayload{Text: "writer remains usable"})}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.commitEventBatch(t.Context(), later); err != nil {
		t.Fatalf("caller cancellation prevented a later commit: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.openSession(t.Context(), writer.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	projection, err := reopened.eventSnapshot()
	if err != nil || len(projection.Events()) != 3 {
		t.Fatalf("restart lost committed facts: %v", err)
	}
}
