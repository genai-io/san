package inspector

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
)

func TestStreamMissingLogReturnsNotFound(t *testing.T) {
	_, _, srv := fixture(t)
	resp, err := srv.Client().Get(srv.URL + "/api/sessions/missing/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing log: got status %d, want 404", resp.StatusCode)
	}
}

func TestStreamStampSkipsUnchangedLog(t *testing.T) {
	_, w, _ := fixture(t)
	var stamp streamStamp
	if !stamp.changed(w.Path()) {
		t.Fatal("first poll must scan")
	}
	if _, err := os.ReadFile(w.Path()); err != nil {
		t.Fatal(err)
	}
	if stamp.changed(w.Path()) {
		t.Fatal("poll after reading unchanged log must skip the scan")
	}
	appendPayload(t, w, session.AnnotationAddPayload{Text: "new"})
	if !stamp.changed(w.Path()) || stamp.changed(w.Path()) {
		t.Fatal("append must trigger exactly one scan")
	}
}

func TestStreamDetectsReplacementWithSameSizeAndMtime(t *testing.T) {
	_, w, srv := fixture(t)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Set a coarse timestamp so it can be copied exactly across both files.
	mtime := time.Unix(1700000000, 0)
	if err := os.Chtimes(w.Path(), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/sessions/session/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(first, "data: ") {
		t.Fatalf("initial batch: %q, %v", first, err)
	}

	// The replacement has no checked prefix, but matches the old size/mtime.
	path := filepath.Join(filepath.Dir(w.Path()), "replacement")
	payload := strings.Repeat("x", int(info.Size())-1) + "\n"
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, w.Path()); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nevent: diagnostic\ndata: {\"error\":\"committed prefix replaced\"}\n\n"; string(rest) != want {
		t.Fatalf("after replacement: got %q, want %q", rest, want)
	}
}

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
