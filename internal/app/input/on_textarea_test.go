package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/core"
)

// The box has to cover every row the textarea actually draws, or the viewport
// silently clips the overflow. Predicting that by counting characters is what
// the textarea's own soft-wrap already does exactly — including word-wrap
// breaks and CJK, where one rune occupies two columns — so this asserts the
// thing that matters: nothing typed goes missing on screen.
func TestTextareaGrowsToFitWrappedContent(t *testing.T) {
	tests := []struct {
		name  string
		width int
		value string
	}{
		{"explicit newlines", 10, "first\nsecond"},
		{"wrapped ascii", 10, "12345678901"},
		{"word wrap near the edge", 12, "aaa aaa aaa aaa"},
		{"wrapped cjk", 74, strings.Repeat("中", 40) + "\n" + strings.Repeat("文", 40)},
		{"mixed width", 20, "hello 世界你好吗今天天气很好"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New("", tt.width, nil, SelectorDeps{})
			m.Textarea.SetValue(tt.value)

			// Soft wrapping inserts row breaks, so compare with all
			// whitespace squeezed out of both sides.
			shown := strings.Join(strings.Fields(xansi.Strip(m.Textarea.View())), "")
			want := strings.Join(strings.Fields(tt.value), "")
			if !strings.Contains(shown, want) {
				t.Fatalf("height %d clips content:\n shown %q\n want  %q", m.Textarea.Height(), shown, want)
			}
		})
	}
}

// A newline at the end opens a row the cursor sits on but no text occupies yet;
// the box still has to grow to show it.
func TestTextareaKeepsCursorRowVisible(t *testing.T) {
	m := New("", 20, nil, SelectorDeps{})
	m.Textarea.SetValue("first\n")

	cursor := m.Textarea.Cursor()
	if cursor == nil {
		t.Fatal("focused composer reported no cursor")
	}
	if m.Textarea.Height() <= cursor.Position.Y {
		t.Fatalf("height %d hides cursor row %d", m.Textarea.Height(), cursor.Position.Y)
	}
}

// The box tops out at half the screen, but the buffer behind it must keep
// accepting input and scroll — MaxHeight alone would refuse keystrokes there.
func TestTextareaAcceptsInputPastVisibleHeight(t *testing.T) {
	m := New("", 40, nil, SelectorDeps{})
	m.SetTerminalHeight(24)

	value := strings.TrimSuffix(strings.Repeat("line\n", 40), "\n")
	m.Textarea.SetValue(value)

	if got := m.Textarea.Value(); got != value {
		t.Fatalf("buffer truncated: got %d lines, want 40", strings.Count(got, "\n")+1)
	}
	if h := m.Textarea.Height(); h > m.maxTextareaHeight() {
		t.Fatalf("visible height %d exceeds cap %d", h, m.maxTextareaHeight())
	}
}

// Newlines are the composer's own binding, not a case in the key router, so the
// keys users press have to actually reach it and split the line.
func TestNewlineKeysInsertNewline(t *testing.T) {
	keys := map[string]tea.KeyPressMsg{
		"shift+enter": {Code: tea.KeyEnter, Mod: tea.ModShift},
		"alt+enter":   {Code: tea.KeyEnter, Mod: tea.ModAlt},
		"ctrl+j":      {Code: 'j', Mod: tea.ModCtrl},
	}

	for name, msg := range keys {
		t.Run(name, func(t *testing.T) {
			m := New("", 40, nil, SelectorDeps{})
			m.Textarea.SetValue("first")
			m.Textarea.CursorEnd()

			m.Textarea, _ = m.Textarea.Update(msg)

			if got := m.Textarea.Value(); got != "first\n" {
				t.Fatalf("%s (%q) gave %q, want %q", name, msg.String(), got, "first\n")
			}
		})
	}

	t.Run("plain enter stays reserved for submit", func(t *testing.T) {
		m := New("", 40, nil, SelectorDeps{})
		m.Textarea.SetValue("first")
		m.Textarea.CursorEnd()

		m.Textarea, _ = m.Textarea.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

		if got := m.Textarea.Value(); got != "first" {
			t.Fatalf("plain enter modified the buffer: %q", got)
		}
	})
}

// The /autopilot and /evolve editors share newChromelessTextarea but appear in
// overlays, where View reports no cursor position — so they must keep painting
// their own.
func TestOverlayEditorsKeepVirtualCursor(t *testing.T) {
	if ta := newChromelessTextarea(); !ta.VirtualCursor() {
		t.Fatal("overlay editors lost their virtual cursor; they would render none")
	}
	if ta := newTextarea(40); ta.VirtualCursor() {
		t.Fatal("composer should drive the real terminal cursor")
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
	m := New("", 80, nil, SelectorDeps{})
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
	m := New("", 80, nil, SelectorDeps{})
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
	m := New("", 80, nil, SelectorDeps{})
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
