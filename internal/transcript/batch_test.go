package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func batchStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir(), "project")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func eventBatch(id string, tail sdksession.Tail) sdksession.Batch {
	return sdksession.Batch{ID: id, Expected: tail, Records: []sdksession.Record{
		{Type: sdksession.TypeApp, Name: "san.events", Data: json.RawMessage(`{"events":[1,2]}`)},
		{Type: sdksession.TypeApp, Name: "san.events", Data: json.RawMessage(`{"events":[3]}`)},
	}}
}

func TestBatchBridgeLeaseLifecycle(t *testing.T) {
	s := batchStore(t)
	ctx := context.Background()
	if _, err := s.CommitBatch(ctx, "one", eventBatch("b", sdksession.Tail{})); !errors.Is(err, ErrUnclaimed) {
		t.Fatal(err)
	}
	if _, err := s.BatchTail(ctx, "one"); !errors.Is(err, ErrUnclaimed) {
		t.Fatal(err)
	}
	if exists, err := s.Exists(ctx, "one"); err != nil || exists {
		t.Fatalf("unclaimed created log: %v %v", exists, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ClaimContext(canceled, "one"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release, err := s.ClaimContext(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := s.Close(); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "one"); err == nil {
		t.Fatal("deleted claimed log")
	}
	other, err := NewStore(s.baseDir, "project")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.ClaimContext(ctx, "one"); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("second lease: %v", err)
	}
	if _, err := s.CommitBatch(canceled, "one", eventBatch("b", sdksession.Tail{})); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.BatchTail(canceled, "one"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	receipt, err := s.CommitBatch(ctx, "one", eventBatch("b", sdksession.Tail{}))
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.BatchTail(ctx, "one")
	if err != nil || tail != receipt.Tail || tail.Count != 2 {
		t.Fatalf("tail %v: %v", tail, err)
	}
	count := 0
	for b, err := range other.Batches(ctx, "one") {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if b.Receipt != receipt || len(b.Batch.Records) != 2 {
			t.Fatalf("partial batch: %+v", b)
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BatchTail(ctx, "one"); !errors.Is(err, ErrUnclaimed) {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitBatch(ctx, "one", eventBatch("after-close", sdksession.Tail{})); !errors.Is(err, ErrUnclaimed) {
		t.Fatal(err)
	}
}

func TestBatchLegacyGuards(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			s := batchStore(t)
			ctx := context.Background()
			release, err := s.Claim("one")
			if err != nil {
				t.Fatal(err)
			}
			b := eventBatch("b", sdksession.Tail{})
			if native {
				data, _ := json.Marshal(Record{Type: SessionStarted, SessionID: "one", Version: SchemaVersion})
				b.Records = []sdksession.Record{{Type: sdksession.TypeApp, Name: SessionStarted, Data: data}, {Type: sdksession.TypeMessage, Message: &ai.Message{ID: "m", Role: "user"}}}
			}
			if _, err := s.CommitBatch(ctx, "one", b); err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(s.TranscriptPath("one"))
			if err := s.Start(ctx, StartCommand{SessionID: "one"}); !errors.Is(err, ErrEventFormat) {
				t.Fatalf("Start: %v", err)
			}
			if _, err := s.Load(ctx, "one"); !errors.Is(err, ErrEventFormat) {
				t.Fatalf("Load: %v", err)
			}
			if err := s.Append(ctx, Record{SessionID: "one", Type: MessageAppended, Message: &ai.Message{ID: "new"}}); !errors.Is(err, ErrEventFormat) {
				t.Fatalf("Append: %v", err)
			}
			if err := s.Fork(ctx, ForkCommand{SourceSessionID: "one", NewSessionID: "fork"}); !errors.Is(err, ErrEventFormat) {
				t.Fatalf("Fork: %v", err)
			}
			if exists, _ := s.Exists(ctx, "fork"); exists {
				t.Fatal("created fork")
			}
			after, _ := os.ReadFile(s.TranscriptPath("one"))
			if !bytes.Equal(before, after) {
				t.Fatal("modified batch log")
			}
		})
	}
}

func TestBatchMalformedPrefixReadOnly(t *testing.T) {
	for _, data := range []string{"{bad json}\n", "{\"batch_version\":99}\n", "{\"type\":\"unknown\"}\n"} {
		t.Run(data, func(t *testing.T) {
			s := batchStore(t)
			if err := os.WriteFile(s.TranscriptPath("bad"), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := s.Start(ctx, StartCommand{SessionID: "bad"}); err == nil {
				t.Fatal("Start accepted malformed prefix")
			}
			if _, err := s.Load(ctx, "bad"); err == nil {
				t.Fatal("Load accepted malformed prefix")
			}
			if err := s.Fork(ctx, ForkCommand{SourceSessionID: "bad", NewSessionID: "fork"}); err == nil {
				t.Fatal("Fork accepted malformed prefix")
			}
			if exists, _ := s.Exists(ctx, "fork"); exists {
				t.Fatal("created destination")
			}
			after, _ := os.ReadFile(s.TranscriptPath("bad"))
			if string(after) != data {
				t.Fatal("repaired malformed prefix")
			}
		})
	}
}

func TestBatchConcurrentReadersAndRelease(t *testing.T) {
	s := batchStore(t)
	ctx := context.Background()
	release, err := s.Claim("one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitBatch(ctx, "one", eventBatch("first", sdksession.Tail{})); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				for b, err := range s.Batches(ctx, "one") {
					if err != nil {
						t.Error(err)
						return
					}
					if len(b.Batch.Records) != 2 {
						t.Error("partial batch")
					}
				}
				if _, err := s.BatchTail(ctx, "one"); err != nil && !errors.Is(err, ErrUnclaimed) {
					t.Error(err)
				}
			}
		})
	}
	wg.Go(func() {
		defer release()
		for i := range 20 {
			tail, err := s.BatchTail(ctx, "one")
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.CommitBatch(ctx, "one", eventBatch(fmt.Sprint(i), tail)); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Wait()
}
