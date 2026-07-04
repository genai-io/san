package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/setting"
)

func TestClassifyContextTier_Boundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		want contextTier
	}{
		{0, tierGood},     // empty context
		{49, tierGood},    // just under warn
		{50, tierGood},    // PRD §7.2: 50 stays good
		{50.01, tierWarn}, // just past good
		{51, tierWarn},
		{79, tierWarn},
		{80, tierWarn}, // PRD §7.2 off-by-one: 80 stays warn
		{81, tierBad},  // only >80 is bad
		{94, tierBad},
		{94.99, tierBad},
		{95, tierCritical}, // PRD §7.2: ≥95 is critical
		{100, tierCritical},
		{120, tierCritical}, // clamp handled upstream; classifier still critical
	}
	for _, c := range cases {
		got := classifyContextTier(c.pct)
		if got != c.want {
			t.Errorf("classifyContextTier(%.2f) = %v, want %v", c.pct, got, c.want)
		}
	}
}

func TestRenderContextBar_FillLevels(t *testing.T) {
	cases := []struct {
		name     string
		used     int
		limit    int
		wantFill int // expected number of '█' characters
		wantPct  string
	}{
		{"empty", 0, 200000, 0, "0%"},
		{"half", 100000, 200000, 5, "50%"},
		{"near-full", 190000, 200000, 10, "95%"}, // 95% rounds to 10 cells (9.5 → 10)
		{"full", 200000, 200000, 10, "100%"},
		{"oversend-clamped", 240000, 200000, 10, "100%"}, // pct > 100 clamped
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderContextBar(c.used, c.limit)
			if strings.Count(got, "█") != c.wantFill {
				t.Errorf("fill cells = %d, want %d (rendered: %q)", strings.Count(got, "█"), c.wantFill, got)
			}
			if !strings.Contains(got, c.wantPct) {
				t.Errorf("pct label %q missing from %q", c.wantPct, got)
			}
		})
	}
}

func TestRenderContextBar_NoLimit(t *testing.T) {
	got := RenderContextBar(100, 0)
	if !strings.Contains(got, "--") {
		t.Errorf("missing '--' label for unknown limit; got %q", got)
	}
	// No fill cells when denominator is unknown.
	if strings.Contains(got, "█") {
		t.Errorf("should not render fill cells for unknown limit; got %q", got)
	}
}

func TestRenderContextBar_NoNegativePercent(t *testing.T) {
	// Defensive: callers should pass non-negative, but the renderer
	// must never emit a negative percentage.
	got := RenderContextBar(-1, 200000)
	if strings.Contains(got, "-") {
		t.Errorf("negative percent rendered: %q", got)
	}
}

func TestRenderContextLabel(t *testing.T) {
	cases := []struct {
		name  string
		used  int
		limit int
		want  string
	}{
		{"compact", 142_000, 200_000, "ctx 142.0k/200.0k"},
		{"millions", 1_500_000, 2_000_000, "ctx 1.5M/2.0M"},
		{"unknown-limit", 5000, 0, "ctx 5.0k/--"}, // used shown, limit unknown
		{"zero-used", 0, 200_000, "ctx 0/200.0k"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderContextLabel(c.used, c.limit)
			visible := stripANSI(got)
			if visible != c.want {
				t.Errorf("RenderContextLabel(%d, %d) visible = %q, want %q (raw: %q)", c.used, c.limit, visible, c.want, got)
			}
		})
	}
}

func TestRenderStableContextLabelKeepsWidth(t *testing.T) {
	a := stripANSI(renderStableContextLabel(8_500, 272_000))
	b := stripANSI(renderStableContextLabel(10_000, 272_000))

	if lipgloss.Width(a) != lipgloss.Width(b) {
		t.Fatalf("stable context labels should have same width: %q (%d), %q (%d)",
			a, lipgloss.Width(a), b, lipgloss.Width(b))
	}
	if !strings.Contains(a, "8.5k/272.0k") || !strings.Contains(b, "10.0k/272.0k") {
		t.Fatalf("stable labels lost expected token text: %q / %q", a, b)
	}
}

func TestRenderCompressionsBadge(t *testing.T) {
	cases := []struct {
		name    string
		n       int
		visible string // empty means badge should not render
	}{
		{"zero-hidden", 0, ""},
		{"one", 1, "compacted ×1"},
		{"four-dim", 4, "compacted ×4"},
		{"five-warn", 5, "compacted ×5"},
		{"nine-warn", 9, "compacted ×9"},
		{"ten-error", 10, "compacted ×10"},
		{"twenty-error", 20, "compacted ×20"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RenderCompressionsBadge(c.n)
			visible := stripANSI(got)
			if c.visible == "" {
				if got != "" {
					t.Errorf("RenderCompressionsBadge(%d) = %q, want empty", c.n, got)
				}
				return
			}
			if visible != c.visible {
				t.Errorf("RenderCompressionsBadge(%d) visible = %q, want %q", c.n, visible, c.visible)
			}
		})
	}
}

func TestFitStatusSegments_DropsInPriorityOrder(t *testing.T) {
	// Priorities: 1=highest (drops last), 4=lowest (drops first).
	segments := []statusSegment{
		{text: "[bar] 71%", priority: 1},
		{text: "ctx 142K/200K", priority: 2},
		{text: "compacted ×2", priority: 3},
		{text: "$0.04", priority: 4},
	}

	// Wide: everything fits, in original order.
	got := fitStatusSegments(segments, 100, 3)
	if len(got) != len(segments) {
		t.Fatalf("wide: kept %d segments, want %d (%q)", len(got), len(segments), got)
	}
	for i, seg := range segments {
		if got[i] != seg.text {
			t.Errorf("wide: position %d = %q, want %q", i, got[i], seg.text)
		}
	}

	// Narrow (~30 cols): lowest-priority segments drop first.
	got = fitStatusSegments(segments, 30, 3)
	joined := strings.Join(got, " · ")
	if !strings.Contains(joined, "[bar] 71%") {
		t.Errorf("priority-1 segment should survive at width 30; got %q", joined)
	}
	if strings.Contains(joined, "$0.04") {
		t.Errorf("priority-4 (cost) should drop first at width 30; got %q", joined)
	}
}

func TestFitStatusSegments_NeverTruncatesMidSegment(t *testing.T) {
	segments := []statusSegment{{text: "exactly12ch", priority: 1}}

	// Just enough room (segment is 11 cols wide).
	if got := fitStatusSegments(segments, 11, 3); len(got) != 1 || got[0] != "exactly12ch" {
		t.Errorf("segment should fit at width=its own width; got %q", got)
	}
	// One col too narrow: drop the whole segment, never truncate.
	if got := fitStatusSegments(segments, 10, 3); len(got) != 0 {
		t.Errorf("segment must drop, not truncate, when it doesn't fit; got %q", got)
	}
}

func TestFitStatusSegments_PreservesOriginalOrder(t *testing.T) {
	// Survivors keep their caller-supplied order even though the highest
	// priority (dropped last) is the second segment.
	segments := []statusSegment{
		{text: "first", priority: 2},
		{text: "second", priority: 1},
	}
	got := fitStatusSegments(segments, 100, 3)
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("survivors should keep original order; got %q", got)
	}
}

func TestFitStatusSegments_AccountsForSeparatorWidth(t *testing.T) {
	// Two 5-col segments need 5 + sepWidth + 5 columns together. With a
	// 3-col separator that's 13; one column short drops the lower-priority
	// (higher-number) second segment.
	segments := []statusSegment{
		{text: "aaaaa", priority: 1},
		{text: "bbbbb", priority: 2},
	}
	if got := fitStatusSegments(segments, 13, 3); len(got) != 2 {
		t.Errorf("both should fit at width 13 (5+3+5); got %q", got)
	}
	if got := fitStatusSegments(segments, 12, 3); len(got) != 1 || got[0] != "aaaaa" {
		t.Errorf("separator should push the second segment out at width 12; got %q", got)
	}
}

func TestRenderOperationModeIndicator_AutoReviewCounts(t *testing.T) {
	cases := []struct {
		name        string
		approvals   int
		escalations int
		wantApprove bool
		wantEscal   bool
	}{
		{"fresh session hides both", 0, 0, false, false},
		{"approvals only", 3, 0, true, false},
		{"escalations only", 0, 2, false, true},
		{"both", 3, 2, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := stripANSI(RenderOperationModeIndicator(setting.ModeAutoReview, c.approvals, c.escalations))
			if !strings.Contains(out, "auto review on") {
				t.Fatalf("missing base label: %q", out)
			}
			if got := strings.Contains(out, "approved"); got != c.wantApprove {
				t.Errorf("approved shown=%v, want %v (out=%q)", got, c.wantApprove, out)
			}
			if got := strings.Contains(out, "escalated"); got != c.wantEscal {
				t.Errorf("escalated shown=%v, want %v (out=%q)", got, c.wantEscal, out)
			}
		})
	}
}

func TestRenderOperationModeIndicator_CountsOnlyInAutoReview(t *testing.T) {
	// Approvals/escalations are an auto-review concept; other modes never show them.
	out := stripANSI(RenderOperationModeIndicator(setting.ModeAutoAccept, 5, 4))
	if strings.Contains(out, "approved") || strings.Contains(out, "escalated") {
		t.Errorf("accept-edits mode must not show review counts: %q", out)
	}
}
