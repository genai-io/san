package reminder

import (
	"reflect"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestWrapEmpty(t *testing.T) {
	if got := Wrap(""); got != "" {
		t.Errorf("Wrap(\"\") = %q, want \"\"", got)
	}
	if got := Wrap("   \n\t "); got != "" {
		t.Errorf("Wrap whitespace-only = %q, want \"\"", got)
	}
}

func TestWrapNonEmpty(t *testing.T) {
	got := Wrap("hello world")
	want := "<system-reminder>\nhello world\n</system-reminder>"
	if got != want {
		t.Errorf("Wrap mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestAppendBlocksPreservesInputAndOrder(t *testing.T) {
	image := ai.ImageBlock(ai.Image{FileName: "example.png"})
	content := ai.Content{ai.TextBlock("hello"), image}
	if got := AppendBlocks(content, ""); !reflect.DeepEqual(got, content) {
		t.Fatalf("empty additions changed content: %+v", got)
	}
	alpha, beta := Wrap("alpha"), Wrap("beta")
	got := AppendBlocks(content, alpha, "", beta)
	want := ai.Content{ai.TextBlock("hello"), image, ai.TextBlock("\n\n" + alpha), ai.TextBlock("\n\n" + beta)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected blocks: %+v", got)
	}
	if got.Text() != "hello\n\n"+alpha+"\n\n"+beta {
		t.Fatalf("text-only fallback lost boundaries: %q", got.Text())
	}
}

func TestBlocksIgnoresMalformedTags(t *testing.T) {
	for name, content := range map[string]string{
		"no reminder at all": "plain user text",
		"unterminated":       "text <system-reminder>body with no close",
		"close only":         "text </system-reminder>",
	} {
		if got := Blocks(content); len(got) != 0 {
			t.Errorf("%s: expected no blocks, got %d", name, len(got))
		}
	}
}

func TestWrapEscapesQuotedTag(t *testing.T) {
	wrapped := Wrap(`const open = "<system-reminder"` + "\n// ends with </system-reminder>")
	if got := Blocks("prompt\n\n" + wrapped); len(got) != 1 || got[0].Text != wrapped {
		t.Fatalf("Blocks() = %+v, want the one reminder whole", got)
	}
}
