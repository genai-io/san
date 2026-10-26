package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
)

func TestRenderUserMessageIndentsEveryLine(t *testing.T) {
	images := []core.Attachment{{Image: ai.Image{FileName: "clip.png"}}}
	for _, tt := range []struct {
		name, text string
		images     []core.Attachment
		gutter     int
	}{
		{"plain", "first\nsecond " + strings.Repeat("word ", 12), nil, lipgloss.Width(InputPrompt)},
		{"attached image", "first\nsecond " + strings.Repeat("word ", 12), images, lipgloss.Width(InputPrompt + "[Image #1] ")},
		{"inline image", "[Image #1] first\nsecond " + strings.Repeat("word ", 12), images, lipgloss.Width(InputPrompt)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(strings.TrimRight(stripANSI(RenderUserMessage(tt.text, tt.text, tt.images, nil, 30)), "\n"), "\n")
			if len(lines) < 4 || !strings.HasPrefix(lines[0], InputPrompt) || !strings.Contains(lines[1], "second") {
				t.Fatalf("expected explicit newline and soft wrapping, got %q", lines)
			}
			for i, line := range lines {
				if lipgloss.Width(line) > 30 || (i > 0 && !strings.HasPrefix(line, strings.Repeat(" ", tt.gutter))) {
					t.Fatalf("line %d lost its width or continuation gutter: %q", i, line)
				}
			}
		})
	}
}

func TestRenderUserMessageWrapsWordsAndKeepsIndentation(t *testing.T) {
	const text = "First line: 中文 alignment wraps correctly.\n\tSecond line: explicit continuation."
	for _, width := range []int{32, 40, 100} {
		got := stripANSI(RenderUserMessage(text, text, nil, nil, width))
		body := strings.TrimPrefix(got, InputPrompt)
		if strings.Join(strings.Fields(body), " ") != strings.Join(strings.Fields(text), " ") {
			t.Fatalf("width %d split a word: %q", width, got)
		}
		if !strings.Contains(got, "\n      Second line:") {
			t.Fatalf("width %d lost explicit line indentation: %q", width, got)
		}
		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n")[1:] {
			if !strings.HasPrefix(line, "  ") || lipgloss.Width(line) > width {
				t.Fatalf("width %d lost continuation alignment: %q", width, line)
			}
		}
	}
}

func TestRenderUserMessagePreservesInlineImagePosition(t *testing.T) {
	rendered := RenderUserMessage(
		"这个图片说了什么 请说一下",
		"[Image #1] 这个图片说了什么 请说一下",
		[]core.Attachment{{Image: ai.Image{FileName: "clip.png"}}},
		nil,
		80,
	)

	imageIdx := strings.Index(rendered, "[Image #1]")
	textIdx := strings.Index(rendered, "这个图片说了什么")
	if imageIdx < 0 || textIdx < 0 {
		t.Fatalf("expected inline image token and text in rendered output: %q", rendered)
	}
	if imageIdx > textIdx {
		t.Fatalf("expected image token to remain before text, got %q", rendered)
	}
}
