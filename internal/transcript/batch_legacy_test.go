package transcript

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func TestBatchBridgeDoesNotConvertLegacy(t *testing.T) {
	s := batchStore(t)
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "legacy"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.TranscriptPath("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.ClaimContext(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := s.BatchTail(ctx, "legacy"); !errors.Is(err, sdksession.ErrMode) {
		t.Fatal(err)
	}
	if _, err := s.CommitBatch(ctx, "legacy", eventBatch("batch", sdksession.Tail{})); !errors.Is(err, sdksession.ErrMode) {
		t.Fatal(err)
	}
	seen := false
	for _, err := range s.Batches(ctx, "legacy") {
		seen = true
		if !errors.Is(err, sdksession.ErrMode) {
			t.Fatal(err)
		}
	}
	if !seen {
		t.Fatal("missing legacy format error")
	}
	after, err := os.ReadFile(s.TranscriptPath("legacy"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("converted legacy bytes", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, err := range s.Batches(canceled, "legacy") {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
