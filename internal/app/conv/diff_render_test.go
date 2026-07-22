package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestChangedSpanFindsMiddle(t *testing.T) {
	oldSpan, newSpan, ok := changedSpan(
		"\t// persisted the enable/disable; the tool registry",
		"\t// persisted the enable/disable to disk. Refresh",
	)
	if !ok {
		t.Fatal("similar lines should produce an emphasis span")
	}
	if oldSpan.from != newSpan.from {
		t.Fatalf("spans should share their prefix boundary: %v vs %v", oldSpan, newSpan)
	}
	oldRunes := []rune("\t// persisted the enable/disable; the tool registry")
	if got := string(oldRunes[oldSpan.from:oldSpan.to]); !strings.Contains(got, "; the tool registry") {
		t.Fatalf("old emphasis = %q", got)
	}
}

func TestChangedSpanRejectsFullRewrite(t *testing.T) {
	if _, _, ok := changedSpan("completely different text", "nothing shared here!!"); ok {
		t.Fatal("lines with no meaningful common affix should not be emphasized")
	}
}

func TestRenderFileDiffNumbersAndMarkers(t *testing.T) {
	diff := "--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n context line\n-removed line\n+added line\n"
	out, hidden := RenderFileDiff(perm.ParseUnifiedDiff(diff), 80, 0)
	if hidden != 0 {
		t.Fatalf("hidden = %d", hidden)
	}
	plain := xansi.Strip(out)
	if !strings.Contains(plain, "   1   context line") {
		t.Fatalf("context row missing, got:\n%s", plain)
	}
	if !strings.Contains(plain, "   2 - removed line") {
		t.Fatalf("removed row missing, got:\n%s", plain)
	}
	if !strings.Contains(plain, "   2 + added line") {
		t.Fatalf("added row missing, got:\n%s", plain)
	}
}

func TestRenderFileDiffCapsRows(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("@@ -1,6 +1,6 @@\n")
	for _, l := range []string{"-a", "-b", "-c", "+d", "+e", "+f"} {
		sb.WriteString(l + "\n")
	}
	out, hidden := RenderFileDiff(perm.ParseUnifiedDiff(sb.String()), 80, 4)
	if hidden != 2 {
		t.Fatalf("hidden = %d, want 2", hidden)
	}
	if rows := strings.Count(xansi.Strip(out), "\n"); rows != 4 {
		t.Fatalf("rendered rows = %d, want 4", rows)
	}
}

func TestRenderFileDiffHidesNoNewlineMarker(t *testing.T) {
	diff := "@@ -1 +1 @@\n-123\n\\ No newline at end of file\n+23\n\\ No newline at end of file\n"
	out, _ := RenderFileDiff(perm.ParseUnifiedDiff(diff), 60, 0)
	plain := xansi.Strip(out)
	if strings.Contains(plain, "No newline") {
		t.Fatalf("no-newline marker is diff bookkeeping and must not render, got:\n%s", plain)
	}
	if !strings.Contains(plain, "1 - 123") || !strings.Contains(plain, "1 + 23") {
		t.Fatalf("diff rows should still render, got:\n%s", plain)
	}
}

func TestRenderFileDiffExpandsTabsInRows(t *testing.T) {
	diff := "@@ -1,1 +1,1 @@\n-\told\n+\tnew\n"
	plain := xansi.Strip(func() string { out, _ := RenderFileDiff(perm.ParseUnifiedDiff(diff), 60, 0); return out }())
	if strings.Contains(plain, "\t") {
		t.Fatalf("tabs must be expanded for stable row padding, got:\n%q", plain)
	}
	if !strings.Contains(plain, "1 -     old") {
		t.Fatalf("expanded removed row missing, got:\n%q", plain)
	}
}
