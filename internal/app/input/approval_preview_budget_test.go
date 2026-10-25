package input

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestApprovalPreviewHeightBudget(t *testing.T) {
	for _, height := range []int{8, 9, 12, 24} {
		p := NewApproval()
		p.Show(&perm.PermissionRequest{ToolName: "Write", Description: strings.Repeat("long preview 中文\n", 80)}, 40, height)
		for screen := 0; screen < 3; screen++ {
			p.screen = screen
			p.pending = approvalOption{AllowAll: true}
			for _, scroll := range []int{0, 1000} {
				p.scroll = scroll
				view := ansi.Strip(p.Render())
				if got := len(strings.Split(view, "\n")); got > height {
					t.Fatalf("height %d screen %d: got %d rows", height, screen, got)
				}
				if !strings.Contains(view, "Enter") {
					t.Fatalf("missing decision hint: %s", view)
				}
			}
		}
	}
}

func TestApprovalNewFileUsesSharedDiff(t *testing.T) {
	meta := perm.GeneratePreview("example.txt", "    indented\n\n"+strings.Repeat("中文", 50), true)
	d := newApprovalDiffPreview(meta, "example.txt")
	d.expanded = true
	lines := append([]perm.DiffLine(nil), meta.Lines...)
	for i := range lines {
		lines[i].Type = perm.DiffLineAdded
	}
	block, _ := conv.RenderFileDiff(lines, 60, 0)
	got := d.render(60)
	for _, line := range meta.Lines {
		if line.Type != perm.DiffLineContext {
			t.Fatal("rendering mutated the permission request's original preview rows")
		}
	}
	if !strings.Contains(got, block) {
		t.Fatal("new-file preview differs from shared renderer")
	}
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "1 +     indented") || !strings.Contains(plain, "2 + ") || strings.Contains(plain, "│") {
		t.Fatalf("lost numbered additions or indentation: %s", plain)
	}
}
