package fs

import (
	"bytes"
	"context"
	"sync"
	"testing"

	coretool "github.com/genai-io/san/internal/tool"
)

func TestOutputProgress_countsAndForwardsVerbatim(t *testing.T) {
	var gotLines int
	var gotBytes int64
	p := &outputProgress{report: func(lines int, byteCount int64) {
		gotLines, gotBytes = lines, byteCount
	}}
	w := p.tee(&bytes.Buffer{})

	// The first write fires the reporter (its throttle window starts fresh).
	if _, err := w.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if gotLines != 2 || gotBytes != 8 {
		t.Fatalf("reported lines=%d bytes=%d, want 2/8", gotLines, gotBytes)
	}

	// A later write keeps accumulating into the totals even while throttled.
	if _, err := w.Write([]byte("three")); err != nil {
		t.Fatal(err)
	}
	if p.lines != 2 || p.bytes != 13 {
		t.Fatalf("accumulated lines=%d bytes=%d, want 2/13", p.lines, p.bytes)
	}
}

func TestOutputProgress_forwardsBytesUnchanged(t *testing.T) {
	var buf bytes.Buffer
	p := &outputProgress{report: func(int, int64) {}}
	w := p.tee(&buf)
	for _, chunk := range []string{"alpha\n", "beta", " gamma\n"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := buf.String(), "alpha\nbeta gamma\n"; got != want {
		t.Fatalf("buffer = %q, want the output verbatim %q", got, want)
	}
}

func TestOutputProgress_teeWithoutReporterIsBareBuffer(t *testing.T) {
	buf := &bytes.Buffer{}
	if w := (&outputProgress{}).tee(buf); w != buf {
		t.Fatalf("tee without a reporter = %T, want the bare buffer (zero overhead)", w)
	}
}

func TestBashExecuteApproved_reportsOutputProgress(t *testing.T) {
	var mu sync.Mutex
	maxLines := 0
	ctx := coretool.ContextWithBashProgress(context.Background(), func(lines int, _ int64) {
		mu.Lock()
		if lines > maxLines {
			maxLines = lines
		}
		mu.Unlock()
	})

	result := (&BashTool{}).ExecuteApproved(ctx, map[string]any{
		"command": `for i in $(seq 1 5); do echo "line $i"; sleep 0.05; done`,
		"timeout": 5000,
	}, t.TempDir())

	if !result.Success {
		t.Fatalf("ExecuteApproved failed: error=%q output=%q", result.Error, result.Output)
	}
	mu.Lock()
	got := maxLines
	mu.Unlock()
	if got < 1 {
		t.Fatalf("progress reporter never observed any line (maxLines=%d)", got)
	}
}
