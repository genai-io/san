package transcript

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestSummaryReadsDoNotOwnAppendLock(t *testing.T) {
	s, err := NewStore(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// A cold listing owns the summary cache lock while reading records. It
	// must not prevent a different operation from updating the writer cache.
	s.summaryMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- s.Append(ctx, Record{SessionID: "s", Type: MessageAppended, Message: &ai.Message{
			ID: "m", Role: ai.RoleUser, Content: ai.TextContent("hello"),
		}})
	}()
	select {
	case err := <-done:
		s.summaryMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		s.summaryMu.Unlock()
		<-done
		t.Fatal("summary cache lock blocked append")
	}
}

func TestHistoryDoesNotOwnProjectionCacheLock(t *testing.T) {
	s, err := NewStore(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.LoadHistory(ctx, "s")
		done <- err
	}()
	select {
	case err := <-done:
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		s.mu.Unlock()
		<-done
		t.Fatal("projection cache lock blocked read-only history")
	}
}
