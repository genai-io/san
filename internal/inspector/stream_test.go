package inspector

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
)

func TestStreamWaitsForCompleteSDKEnvelope(t *testing.T) {
	_, w, srv := fixture(t)
	appendPayload(t, w, session.AnnotationAddPayload{Text: "complete only"})
	bytes, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	// A partial frame contains the native event but is not a committed batch.
	if err = os.WriteFile(w.Path(), bytes[:len(bytes)-2], 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/sessions/session/stream", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stream := make(chan string, 8)
	go func() {
		defer close(stream)
		scan := bufio.NewScanner(resp.Body)
		for scan.Scan() {
			if strings.HasPrefix(scan.Text(), "data: ") {
				stream <- strings.TrimPrefix(scan.Text(), "data: ")
			}
		}
	}()
	select {
	case first := <-stream:
		var b rawBatch
		if err = json.Unmarshal([]byte(first), &b); err != nil || b.Tail.Sequence != 1 {
			t.Fatalf("first batch %s %v", first, err)
		}
	case <-ctx.Done():
		t.Fatal("initial batch missing")
	}
	select {
	case early := <-stream:
		t.Fatalf("uncommitted envelope leaked: %s", early)
	case <-time.After(700 * time.Millisecond):
	}
	f, err := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(bytes[len(bytes)-2:]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	select {
	case final := <-stream:
		var b rawBatch
		if err = json.Unmarshal([]byte(final), &b); err != nil || b.Tail.Sequence != 2 {
			t.Fatalf("final batch %s %v", final, err)
		}
	case <-ctx.Done():
		t.Fatal("completed batch missing")
	}
}

func TestStreamReportsCorruptionAfterCheckedPrefix(t *testing.T) {
	_, w, srv := fixture(t)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("{broken}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	resp, err := srv.Client().Get(srv.URL + "/api/sessions/session/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	event := strings.Index(text, "data: ")
	diagnostic := strings.Index(text, "event: diagnostic")
	if event < 0 || diagnostic < event {
		t.Fatalf("checked prefix hidden or reordered: %s", text)
	}
}
