package input

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestApprovalPreviewCacheMatchesFreshRender(t *testing.T) {
	oldProfile, oldDark := lipgloss.Writer.Profile, kit.IsDarkBackground()
	lipgloss.Writer.Profile = colorprofile.TrueColor
	kit.InitTheme("dark")
	t.Cleanup(func() {
		lipgloss.Writer.Profile = oldProfile
		if oldDark {
			kit.InitTheme("dark")
		} else {
			kit.InitTheme("light")
		}
	})
	request := &perm.PermissionRequest{ToolName: "Write", FilePath: "example.txt", CallerAgent: "long-configured-reviewer-name",
		WorkingDirectory: "/tmp/fixture", PermissionMode: "default", AllowRules: []string{"Write(example.txt)"},
		DiffMeta: perm.GeneratePreview("example.txt", strings.Repeat("    indented 中文 content\n", 50), true)}
	p := NewApproval()
	p.Show(request, 40, 12)
	check := func() {
		t.Helper()
		got := p.Render()
		fresh := NewApproval()
		fresh.Show(request, p.width, p.height)
		fresh.screen, fresh.pending, fresh.selectedIdx, fresh.scroll = p.screen, p.pending, p.selectedIdx, p.scroll
		if want := fresh.Render(); got != want {
			t.Fatalf("cached preview differs from fresh layout: got %q, want %q", got, want)
		}
	}
	check()
	cached := &p.previewLines[0]
	p.selectedIdx, p.scroll = 1, 2
	check()
	if &p.previewLines[0] != cached {
		t.Fatal("menu navigation or scrolling rebuilt the preview")
	}
	p.Resize(32, 12)
	check()
	p.Resize(32, 6)
	check()
	p.screen, p.pending = 2, approvalOption{AllowAll: true}
	check()
	p.pending = approvalOption{Persist: true}
	check()
	p.screen = 0
	check()
	kit.InitTheme("light")
	check()
	lipgloss.Writer.Profile = 0
	check()
	request = &perm.PermissionRequest{ToolName: "Write", Description: "replacement-operation"}
	p.Show(request, 32, 12)
	check()
	if !strings.Contains(p.Render(), "replacement-operation") {
		t.Fatal("replacement request reused old preview")
	}
	p.Hide()
	if p.previewLines != nil {
		t.Fatal("closed approval retained preview")
	}
}

func TestApprovalSessionExplanationWrapsWords(t *testing.T) {
	const explanation = "Allow future calls to Bash for this session, beyond this command. This does not allow all tools. Safety checks and explicit ask rules still apply."
	for _, width := range []int{31, 39, 99} {
		p := NewApproval()
		p.Show(&perm.PermissionRequest{ToolName: "Bash"}, width, 24)
		p.screen, p.pending = 2, approvalOption{AllowAll: true}
		lines := p.wrappedPreview(width, false)
		if got := strings.Join(strings.Fields(strings.Join(lines, "\n")), " "); got != explanation {
			t.Fatalf("width %d split a word in permission explanation: %q", width, got)
		}
	}
}

func BenchmarkApprovalPreviewRender(b *testing.B) {
	request := &perm.PermissionRequest{ToolName: "Write", FilePath: "example.txt",
		DiffMeta: perm.GeneratePreview("example.txt", strings.Repeat("    indented 中文 content\n", 1000), true)}
	for _, cached := range []bool{false, true} {
		name := "rebuild"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			p := NewApproval()
			p.Show(request, 80, 24)
			p.Render()
			b.ReportAllocs()
			for b.Loop() {
				if !cached {
					p.previewLines = nil
				}
				p.Render()
			}
		})
	}
}
