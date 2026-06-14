package input

import (
	"testing"

	"github.com/genai-io/san/internal/app/selector"
	"github.com/genai-io/san/internal/core"
)

func TestUpdateHeightUsesExplicitAndWrappedLines(t *testing.T) {
	m := New("", 10, nil, selector.Registries{})
	m.Textarea.SetValue("first\nsecond")
	m.UpdateHeight()
	if got := m.Textarea.Height(); got != 2 {
		t.Fatalf("height for explicit multiline input = %d, want 2", got)
	}

	m.Textarea.SetValue("12345678901")
	m.UpdateHeight()
	if got := m.Textarea.Height(); got != 2 {
		t.Fatalf("height for wrapped input = %d, want 2", got)
	}
}

func Test_imageRefPattern(t *testing.T) {
	tests := []struct {
		input    string
		expected [][]string
	}{
		{
			input:    "describe @image.png",
			expected: [][]string{{"@image.png", "image.png", "png"}},
		},
		{
			input:    "@photo.jpg analyze this",
			expected: [][]string{{"@photo.jpg", "photo.jpg", "jpg"}},
		},
		{
			input:    "compare @a.png with @b.jpeg",
			expected: [][]string{{"@a.png", "a.png", "png"}, {"@b.jpeg", "b.jpeg", "jpeg"}},
		},
		{
			input:    "no images here",
			expected: nil,
		},
		{
			input:    "@path/to/image.webp",
			expected: [][]string{{"@path/to/image.webp", "path/to/image.webp", "webp"}},
		},
		{
			input:    "@animated.gif",
			expected: [][]string{{"@animated.gif", "animated.gif", "gif"}},
		},
		{
			input:    "@document.md is not an image",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			matches := imageRefPattern.FindAllStringSubmatch(tt.input, -1)
			if len(matches) != len(tt.expected) {
				t.Errorf("FindAllStringSubmatch(%q) got %d matches, want %d", tt.input, len(matches), len(tt.expected))
				return
			}
			for i, match := range matches {
				for j, part := range match {
					if j < len(tt.expected[i]) && part != tt.expected[i][j] {
						t.Errorf("match[%d][%d] = %q, want %q", i, j, part, tt.expected[i][j])
					}
				}
			}
		})
	}
}

func TestPendingImageMatchesAndExtractInlineImages(t *testing.T) {
	m := New("", 80, nil, selector.Registries{})
	first := m.AddPendingImage(core.Image{FileName: "a.png"})
	second := m.AddPendingImage(core.Image{FileName: "b.png"})

	m.Textarea.SetValue(second + " alpha " + first + " omega")

	matches := m.PendingImageMatches()
	if len(matches) != 2 {
		t.Fatalf("expected 2 inline image matches, got %d", len(matches))
	}
	if matches[0].ID != 2 || matches[1].ID != 1 {
		t.Fatalf("expected matches in text order, got %#v", matches)
	}

	content, images := m.ExtractInlineImages(m.Textarea.Value())
	if content != "alpha  omega" {
		t.Fatalf("unexpected content after extraction: %q", content)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	if images[0].FileName != "b.png" || images[1].FileName != "a.png" {
		t.Fatalf("unexpected image extraction order: %#v", images)
	}
}

func TestExtractInlineImagesUsesSubmittedBufferOffsets(t *testing.T) {
	m := New("", 80, nil, selector.Registries{})
	label := m.AddPendingImage(core.Image{FileName: "a.png"})

	raw := "  " + label + " hi"
	m.Textarea.SetValue(raw)

	content, images := m.ExtractInlineImages("[" + raw[2:])
	if content != "[ hi" {
		t.Fatalf("unexpected content after extraction: %q", content)
	}
	if len(images) != 1 || images[0].FileName != "a.png" {
		t.Fatalf("unexpected extracted images: %#v", images)
	}
}

func TestRemoveImageToken(t *testing.T) {
	m := New("", 80, nil, selector.Registries{})
	label := m.AddPendingImage(core.Image{FileName: "clip.png"})
	m.Textarea.SetValue("hello " + label + " world")

	match, ok := m.MatchAdjacentToCursor(len([]rune("hello "+label)), false)
	if !ok {
		t.Fatal("expected image token match at cursor")
	}

	m.RemoveImageToken(match, len([]rune("hello ")))

	if got := m.Textarea.Value(); got != "hello  world" {
		t.Fatalf("unexpected textarea value after token removal: %q", got)
	}
	if len(m.Images.Pending) != 0 {
		t.Fatalf("expected pending images to be cleared, got %d", len(m.Images.Pending))
	}
	if m.CursorIndex() != len([]rune("hello ")) {
		t.Fatalf("unexpected cursor position after removal: %d", m.CursorIndex())
	}
}
