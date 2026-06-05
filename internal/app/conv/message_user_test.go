package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/san/v2/internal/core"
)

func TestRenderUserMessagePreservesInlineImagePosition(t *testing.T) {
	rendered := RenderUserMessage(
		"这个图片说了什么 请说一下",
		"[Image #1] 这个图片说了什么 请说一下",
		[]core.Image{{FileName: "clip.png"}},
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
