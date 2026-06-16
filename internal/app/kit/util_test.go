package kit

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTruncateTextNeverExceedsDisplayWidth(t *testing.T) {
	// Mixed CJK (2 cols each) + ASCII — the case that overflowed the skill
	// selector when truncation counted runes instead of display columns.
	const desc = "配音 / voiceover / narration / dub — generate dynamically-paced " +
		"TTS narration (节奏 + 抑扬顿挫) from a script, with an optional synthesized " +
		"ambient music bed and sidechain ducking"

	for _, maxLen := range []int{8, 20, 40, 80, 158} {
		got := TruncateText(desc, maxLen)
		if w := lipgloss.Width(got); w > maxLen {
			t.Errorf("TruncateText(maxLen=%d) width = %d, must be <= %d (got %q)", maxLen, w, maxLen, got)
		}
	}
}

func TestTruncateTextKeepsShortTextAndAddsEllipsis(t *testing.T) {
	if got := TruncateText("hello", 10); got != "hello" {
		t.Errorf("fitting text changed: got %q, want %q", got, "hello")
	}
	if got := TruncateText("", 5); got != "" {
		t.Errorf("empty text: got %q, want empty", got)
	}
	if got := TruncateText("hello world", 0); got != "hello world" {
		t.Errorf("maxLen<=0 should return original: got %q", got)
	}
	// ASCII truncation still cuts to maxLen columns with a trailing ellipsis.
	got := TruncateText("hello world", 8)
	if lipgloss.Width(got) > 8 {
		t.Errorf("width = %d, want <= 8 (got %q)", lipgloss.Width(got), got)
	}
	if []rune(got)[len([]rune(got))-1] != '…' {
		t.Errorf("expected trailing ellipsis, got %q", got)
	}
}
