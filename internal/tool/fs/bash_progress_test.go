package fs

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	coretool "github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestOutputProgress_countsAndForwardsVerbatim(t *testing.T) {
	var gotLines int
	var gotBytes int64
	var gotTail string
	p := &outputProgress{report: func(lines int, byteCount int64, tail string) {
		gotLines, gotBytes, gotTail = lines, byteCount, tail
	}}
	w := p.tee(&bytes.Buffer{})

	// The first write fires the reporter (its throttle window starts fresh).
	if _, err := w.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if gotLines != 2 || gotBytes != 8 || gotTail != "one\ntwo\n" {
		t.Fatalf("reported lines=%d bytes=%d tail=%q, want 2/8 and the output", gotLines, gotBytes, gotTail)
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
	p := &outputProgress{report: func(int, int64, string) {}}
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

// With nobody listening the output's end is still kept: the result carries it.
func TestOutputProgress_keepsTailWithoutReporter(t *testing.T) {
	p := &outputProgress{}
	w := p.tee(&bytes.Buffer{})
	if _, err := w.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if got := p.lastTail(); got != "one\ntwo\n" {
		t.Fatalf("tail = %q, want the output's end", got)
	}
}

func TestBashExecuteApproved_reportsOutputProgress(t *testing.T) {
	var mu sync.Mutex
	maxLines := 0
	ctx := coretool.ContextWithBashProgress(context.Background(), func(lines int, _ int64, _ string) {
		mu.Lock()
		if lines > maxLines {
			maxLines = lines
		}
		mu.Unlock()
	})

	result := bashTool(t).ExecuteApproved(ctx, map[string]any{
		"command": `for i in $(seq 1 5); do echo "line $i"; sleep 0.05; done`,
		"timeout": 30000,
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

// The tail keeps only the output's last progressTailBytes, however large one
// write is, so a report never carries a command's whole output.
func TestOutputProgress_tailKeepsTheEnd(t *testing.T) {
	var gotTail string
	p := &outputProgress{report: func(_ int, _ int64, tail string) { gotTail = tail }}
	big := bytes.Repeat([]byte("x"), 3*progressTailBytes)
	if _, err := p.tee(&bytes.Buffer{}).Write(append(big, "end\n"...)); err != nil {
		t.Fatal(err)
	}
	if len(gotTail) != progressTailBytes || !strings.HasSuffix(gotTail, "end\n") {
		t.Fatalf("tail is %d bytes ending %q, want %d ending in the output's end", len(gotTail), gotTail[max(0, len(gotTail)-8):], progressTailBytes)
	}
}

// Output that stops inside a throttle window is still reported once the window
// ends, not left at the last report until the command finishes.
func TestOutputProgress_reportsWhatTheThrottleHeldBack(t *testing.T) {
	reports := make(chan int, 4)
	p := &outputProgress{report: func(lines int, _ int64, _ string) { reports <- lines }}
	w := p.tee(&bytes.Buffer{})
	_, _ = w.Write([]byte("one\n"))
	_, _ = w.Write([]byte("two\nthree\n"))
	if got := <-reports; got != 1 {
		t.Fatalf("first report = %d lines, want 1", got)
	}
	select {
	case got := <-reports:
		if got != 3 {
			t.Fatalf("held-back report = %d lines, want 3", got)
		}
	case <-time.After(5 * progressInterval):
		t.Fatal("the throttled write was never reported")
	}
	p.stop()
}

// Past the 30 KB the model gets, the result still counts every line, and a
// failure keeps the real end as stdout and stderr interleaved, so its finished
// row shows what the live one did. A success keeps a short output whole and a
// long one no end: it folds.
func TestBashExecuteApproved_truncatedOutputKeepsCountAndEnd(t *testing.T) {
	ctx := coretool.ContextWithBashProgress(context.Background(), func(int, int64, string) {})
	result := bashTool(t).ExecuteApproved(ctx, map[string]any{
		"command": `for i in $(seq 1 8000); do echo "line $i"; done; exit 1`,
		"timeout": 30000,
	}, t.TempDir())
	d, ok := result.Details.(toolresult.BashDetails)
	if result.Success || !ok {
		t.Fatalf("success=%v details=%#v", result.Success, result.Details)
	}
	if d.LineCount != 8000 || !strings.HasSuffix(d.Tail, "line 7999\nline 8000\n") {
		t.Fatalf("lineCount=%d tail ends %q, want 8000 and the real end", d.LineCount, d.Tail[max(0, len(d.Tail)-20):])
	}
	if lines := strings.Count(d.Tail, "\n"); lines != previewTailLines {
		t.Fatalf("tail has %d lines, want the %d a row can preview", lines, previewTailLines)
	}
	mixed := bashTool(t).ExecuteApproved(ctx, map[string]any{"command": "echo a; sleep 0.05; echo err >&2; sleep 0.05; echo b; exit 1", "timeout": 30000}, t.TempDir())
	if d := mixed.Details.(toolresult.BashDetails); d.LineCount != 3 || d.Tail != "a\nerr\nb\n" {
		t.Fatalf("uncut failure: %#v, want 3 lines in the order written", d)
	}
	short := bashTool(t).ExecuteApproved(ctx, map[string]any{"command": "echo hi", "timeout": 30000}, t.TempDir())
	if d := short.Details.(toolresult.BashDetails); d.LineCount != 1 || d.Tail != "hi\n" {
		t.Fatalf("short success: %#v, want its count and whole output", d)
	}
	long := bashTool(t).ExecuteApproved(ctx, map[string]any{"command": "seq 1 100", "timeout": 30000}, t.TempDir())
	if d := long.Details.(toolresult.BashDetails); d.LineCount != 100 || d.Tail != "" {
		t.Fatalf("long success: %#v, want its count and no end", d)
	}
}
