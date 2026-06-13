package conv

import (
	"regexp"
	"strings"
	"testing"
)

// stripANSI removes ANSI escape sequences (CSI and OSC 8 hyperlinks) from a string.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\]8;;[^\x1b]*\x1b\\`)

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

func TestMDRenderer_Heading(t *testing.T) {
	r := NewMDRenderer(80)

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"h1", "# Hello", "Hello"},
		{"h2", "## World", "World"},
		{"h3", "### Details", "Details"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := r.Render(tt.input)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			if !strings.Contains(out, tt.contains) {
				t.Errorf("output %q should contain %q", out, tt.contains)
			}
		})
	}
}

func TestMDRenderer_Emphasis(t *testing.T) {
	r := NewMDRenderer(80)

	t.Run("bold", func(t *testing.T) {
		out, err := r.Render("**bold text**")
		if err != nil {
			t.Fatalf("Render error: %v", err)
		}
		plain := stripANSI(out)
		if !strings.Contains(plain, "bold text") {
			t.Errorf("output should contain 'bold text', got:\n%s", plain)
		}
	})

	t.Run("italic", func(t *testing.T) {
		out, err := r.Render("*italic text*")
		if err != nil {
			t.Fatalf("Render error: %v", err)
		}
		plain := stripANSI(out)
		if !strings.Contains(plain, "italic text") {
			t.Errorf("output should contain 'italic text', got:\n%s", plain)
		}
	})
}

func TestMDRenderer_CodeSpan(t *testing.T) {
	r := NewMDRenderer(80)

	out, err := r.Render("Use `fmt.Println` here")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out, "fmt.Println") {
		t.Errorf("output %q should contain 'fmt.Println'", out)
	}
}

func TestMDRenderer_FencedCodeBlock(t *testing.T) {
	r := NewMDRenderer(80)

	input := "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "func main()") {
		t.Errorf("output should contain 'func main()', got:\n%s", plain)
	}
	// Code block should be padded for visual distinction
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "func") {
			if !strings.HasPrefix(line, " ") {
				t.Errorf("code line should be padded: %q", line)
			}
			break
		}
	}
}

func TestMDRenderer_UnorderedList(t *testing.T) {
	r := NewMDRenderer(80)

	input := "- item one\n- item two\n- item three"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "item one") {
		t.Errorf("output should contain 'item one', got:\n%s", plain)
	}
	if !strings.Contains(plain, "item two") {
		t.Errorf("output should contain 'item two', got:\n%s", plain)
	}
	if !strings.Contains(plain, "•") {
		t.Errorf("output should contain bullet character '•'")
	}
}

func TestMDRenderer_OrderedList(t *testing.T) {
	r := NewMDRenderer(80)

	input := "1. first\n2. second\n3. third"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "first") {
		t.Errorf("output should contain 'first', got:\n%s", plain)
	}
	if !strings.Contains(plain, "1.") {
		t.Errorf("output should contain '1.'")
	}
}

func TestMDRenderer_Link(t *testing.T) {
	r := NewMDRenderer(80)

	input := "Visit [Go](https://golang.org) for info"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "Go") {
		t.Errorf("output should contain link text 'Go', got:\n%s", plain)
	}
}

func TestMDRenderer_ThematicBreak(t *testing.T) {
	r := NewMDRenderer(80)

	input := "above\n\n---\n\nbelow"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "above") || !strings.Contains(plain, "below") {
		t.Errorf("output should contain text above and below the rule, got:\n%s", plain)
	}
	if !strings.Contains(plain, "---") && !strings.Contains(plain, "─") {
		t.Errorf("output should contain horizontal rule, got:\n%s", plain)
	}
}

func TestMDRenderer_Blockquote(t *testing.T) {
	r := NewMDRenderer(80)

	input := "> This is a quote"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "This is a quote") {
		t.Errorf("output should contain quote text, got:\n%s", plain)
	}
}

func TestMDRenderer_Paragraph(t *testing.T) {
	r := NewMDRenderer(80)

	input := "Hello world, this is a test paragraph."
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out, "Hello world") {
		t.Errorf("output should contain paragraph text")
	}
}

func TestMDRenderer_WordWrap(t *testing.T) {
	r := NewMDRenderer(40) // narrow width

	input := "This is a long paragraph that should wrap at the specified width boundary for proper terminal display."
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Errorf("expected word wrap to produce multiple lines, got %d", len(lines))
	}
}

func TestMDRenderer_MixedContent(t *testing.T) {
	r := NewMDRenderer(80)

	input := `# Title

Some **bold** and *italic* text with ` + "`code`" + `.

- item 1
- item 2

` + "```go\nfmt.Println(\"hi\")\n```"

	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}

	plain := stripANSI(out)
	checks := []string{"Title", "bold", "italic", "code", "item 1", "item 2", "Println"}
	for _, check := range checks {
		if !strings.Contains(plain, check) {
			t.Errorf("mixed content output should contain %q, got:\n%s", check, plain)
		}
	}
}

func Test_renderMarkdownContent(t *testing.T) {
	r := NewMDRenderer(80)

	result := renderMarkdownContent(r, "# Hello\n\nWorld")
	if !strings.Contains(result, "Hello") {
		t.Errorf("result should contain 'Hello'")
	}
	if !strings.Contains(result, "World") {
		t.Errorf("result should contain 'World'")
	}
	// Should be trimmed
	if strings.HasPrefix(result, "\n") || strings.HasSuffix(result, "\n") {
		t.Errorf("result should be trimmed, got: %q", result)
	}
}

func TestNormalizeLineBreaks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "joins paragraph lines",
			input: "This is line one.\nThis is line two.\nThis is line three.",
			want:  "This is line one. This is line two. This is line three.",
		},
		{
			name:  "preserves paragraph breaks",
			input: "Paragraph one.\n\nParagraph two.",
			want:  "Paragraph one.\n\nParagraph two.",
		},
		{
			name:  "preserves headers",
			input: "# Header\nSome text.\nMore text.",
			want:  "# Header\nSome text. More text.",
		},
		{
			name:  "preserves list items",
			input: "- item one\n- item two\n- item three",
			want:  "- item one\n- item two\n- item three",
		},
		{
			name:  "preserves ordered list",
			input: "1. first\n2. second\n3. third",
			want:  "1. first\n2. second\n3. third",
		},
		{
			name:  "preserves code blocks",
			input: "```go\nfunc main() {\n  fmt.Println(\"hello\")\n}\n```",
			want:  "```go\nfunc main() {\n  fmt.Println(\"hello\")\n}\n```",
		},
		{
			name:  "preserves blockquotes",
			input: "> quote line one\n> quote line two",
			want:  "> quote line one\n> quote line two",
		},
		{
			name:  "preserves hard breaks (trailing spaces)",
			input: "line one  \nline two",
			want:  "line one  \nline two",
		},
		{
			name:  "preserves indented code blocks",
			input: "    code line 1\n    code line 2",
			want:  "    code line 1\n    code line 2",
		},
		{
			name:  "LLM-style wrapped paragraph",
			input: "This is a long description that the LLM wrapped at 80\ncolumns, but the terminal is much wider so it should\nbe reflowed.",
			want:  "This is a long description that the LLM wrapped at 80 columns, but the terminal is much wider so it should be reflowed.",
		},
		{
			name:  "mixed content",
			input: "# Title\n\nFirst paragraph that wraps\nat 80 columns.\n\n- list item\n- another item\n\nAnother paragraph\nthat also wraps.",
			want:  "# Title\n\nFirst paragraph that wraps at 80 columns.\n\n- list item\n- another item\n\nAnother paragraph that also wraps.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLineBreaks(tt.input)
			if got != tt.want {
				t.Errorf("normalizeLineBreaks()\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestMDRenderer_Table(t *testing.T) {
	r := NewMDRenderer(80)

	input := "| Name | Value |\n|------|-------|\n| foo  | bar   |\n| baz  | qux   |"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)

	// Should contain table content
	if !strings.Contains(plain, "Name") {
		t.Errorf("table should contain 'Name', got:\n%s", plain)
	}
	if !strings.Contains(plain, "foo") {
		t.Errorf("table should contain 'foo', got:\n%s", plain)
	}
	// Should have internal separators
	if !strings.Contains(plain, "│") {
		t.Errorf("table should have column separators │, got:\n%s", plain)
	}
	if !strings.Contains(plain, "─") {
		t.Errorf("table should have row separators ─, got:\n%s", plain)
	}
}

func Test_isAllTableLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"header only", "| Name | Value |\n", true},
		{"header + separator", "| Name | Value |\n|------|-------|\n", true},
		{"full table", "| Name |\n|---|\n| val |\n", true},
		{"table + trailing blank", "| Name |\n|---|\n| val |\n\n", true},
		{"non-table text", "Hello world\n", false},
		{"table then paragraph", "| Name |\n|---|\nParagraph\n", false},
		{"empty string", "", false},
		{"just blank lines", "\n\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllTableLines(tt.input)
			if got != tt.want {
				t.Errorf("isAllTableLines(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMDRenderer_TableWithLinks(t *testing.T) {
	r := NewMDRenderer(80)

	input := "| Name | Link |\n|------|------|\n| Go | [Go](https://golang.org) |\n| Rust | [Rust](https://rust-lang.org) |"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)

	if !strings.Contains(plain, "Go") {
		t.Errorf("table should contain link text 'Go', got:\n%s", plain)
	}
	if !strings.Contains(plain, "Rust") {
		t.Errorf("table should contain link text 'Rust', got:\n%s", plain)
	}
	if strings.Contains(plain, "[Go]") {
		t.Errorf("table should not contain raw markdown link syntax '[Go]', got:\n%s", plain)
	}
	if strings.Contains(plain, "https://golang.org") {
		t.Errorf("table should not display URL as plain text, got:\n%s", plain)
	}
	if !strings.Contains(out, "\x1b]8;;https://golang.org\x1b\\") {
		t.Errorf("table should contain OSC 8 hyperlink escape for golang.org, got:\n%s", out)
	}
}

func TestRenderInlineMarkdown_Link(t *testing.T) {
	out := renderInlineMarkdown("[example](https://example.com)")
	plain := stripANSI(out)

	if !strings.Contains(plain, "example") {
		t.Errorf("should contain link text 'example', got: %s", plain)
	}
	if strings.Contains(plain, "https://example.com") {
		t.Errorf("should not display URL as plain text, got: %s", plain)
	}
	if strings.Contains(plain, "[example]") {
		t.Errorf("should not contain raw markdown syntax, got: %s", plain)
	}
	if !strings.Contains(out, "\x1b]8;;https://example.com\x1b\\") {
		t.Errorf("should contain OSC 8 hyperlink escape, got: %s", out)
	}
}

func TestRender_Markdown_NestedList(t *testing.T) {
	r := NewMDRenderer(80)

	input := "- parent one\n  - child a\n  - child b\n- parent two"
	out, err := r.Render(input)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := stripANSI(out)

	if !strings.Contains(plain, "parent one") {
		t.Errorf("output should contain 'parent one', got:\n%s", plain)
	}
	if !strings.Contains(plain, "parent two") {
		t.Errorf("output should contain 'parent two', got:\n%s", plain)
	}
	if !strings.Contains(plain, "child a") {
		t.Errorf("output should contain nested item 'child a', got:\n%s", plain)
	}
	if !strings.Contains(plain, "child b") {
		t.Errorf("output should contain nested item 'child b', got:\n%s", plain)
	}
}

func TestRender_EmptyMessage_NoOutput(t *testing.T) {
	r := NewMDRenderer(80)

	out, err := r.Render("")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	plain := strings.TrimSpace(stripANSI(out))
	if plain != "" {
		t.Errorf("expected empty output for empty input, got %q", plain)
	}
}

func TestMDRenderer_NoLeadingBlankLine(t *testing.T) {
	r := NewMDRenderer(80)
	tests := []struct {
		name  string
		input string
	}{
		{"paragraph", "Hello world."},
		{"heading", "## Title"},
		{"list", "- item 1\n- item 2"},
		{"code block", "```go\nfunc main() {}\n```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := r.Render(tt.input)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			if strings.HasPrefix(out, "\n") {
				t.Errorf("output should not start with blank line, got:\n%q", out)
			}
		})
	}
}

func TestMDRenderer_NoConsecutiveBlankLines(t *testing.T) {
	r := NewMDRenderer(80)
	tests := []struct {
		name  string
		input string
	}{
		{"heading + paragraph", "## Title\n\nSome content here."},
		{"paragraph + list", "Here are items:\n\n- Item 1\n- Item 2"},
		{"paragraph + code", "Code:\n\n```go\nfmt.Println(\"hi\")\n```"},
		{"heading + list", "## Summary\n\n- Item 1\n- Item 2"},
		{"multi section", "## S1\n\nContent 1.\n\n## S2\n\nContent 2."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := r.Render(tt.input)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}
			if strings.Contains(out, "\n\n\n") {
				plain := stripANSI(out)
				t.Errorf("output should not contain consecutive blank lines, got:\n%s", plain)
			}
		})
	}
}
