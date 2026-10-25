package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

func approvalKey(p *ApprovalModel, s string) *ApprovalResponseMsg {
	var k tea.KeyPressMsg
	switch s {
	case "enter":
		k.Code = tea.KeyEnter
	case "esc":
		k.Code = tea.KeyEscape
	default:
		k.Code = []rune(s)[0]
		k.Text = s
	}
	_, r := p.handleKeypress(k)
	return r
}
func TestApprovalScopeConfirmation(t *testing.T) {
	for _, persist := range []bool{false, true} {
		p := NewApproval()
		req := &perm.PermissionRequest{ToolName: "Bash", AllowRules: []string{"Bash(ls)", "Bash(pwd)"}}
		p.Show(req, 40, 10)
		if approvalKey(&p, "3") != nil || p.screen != 1 {
			t.Fatal("more granted")
		}
		key := "2"
		if persist {
			key = "3"
		}
		if approvalKey(&p, key) != nil || p.screen != 2 || p.selectedIdx != 0 {
			t.Fatal("scope skipped confirmation")
		}
		view := p.Render()
		if persist && !strings.Contains(view, "Bash(ls)") {
			t.Fatal(view)
		}
		r := approvalKey(&p, "2")
		if r == nil || r.Request != req || r.Persist != persist || r.AllowAll == persist {
			t.Fatalf("response %+v", r)
		}
	}
}
func TestApprovalNavigationAndReplacement(t *testing.T) {
	p := NewApproval()
	req := &perm.PermissionRequest{ToolName: "Bash"}
	p.Show(req, 32, 12)
	if _, r := p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}); r != nil {
		t.Fatal("shift-tab grants")
	}
	approvalKey(&p, "3")
	approvalKey(&p, "2")
	replacement := &perm.PermissionRequest{ToolName: "Write", OnceOnly: true}
	p.Show(replacement, 32, 12)
	if p.screen != 0 || p.pending.AllowAll || len(p.options()) != 2 {
		t.Fatal("stale state")
	}
	if _, r := p.handleKeypress(tea.KeyPressMsg{Code: 'y', Text: "y", IsRepeat: true}); r != nil {
		t.Fatal("repeat grants")
	}
	r := approvalKey(&p, "y")
	if r == nil || r.Request != replacement || r.AllowAll || r.Persist {
		t.Fatal(r)
	}
}
func TestApprovalCommandScrollableAtSmallSizes(t *testing.T) {
	for _, size := range [][2]int{{32, 12}, {40, 10}} {
		p := NewApproval()
		command := "echo " + strings.Repeat("abcdefghij", 30) + "\n    final-command"
		p.Show(&perm.PermissionRequest{ToolName: "Bash", BashMeta: &perm.BashMetadata{Command: command}}, size[0], size[1])
		seen := ""
		for i := 0; i < 30; i++ {
			view := ansi.Strip(p.Render())
			if len(strings.Split(view, "\n")) > size[1] {
				t.Fatal(view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatal(line)
				}
			}
			seen += view
			p.handleKeypress(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		}
		if !strings.Contains(seen, "final-command") {
			t.Fatal("command tail unreachable")
		}
		full := ansi.Strip(p.bashPreview.render(size[0]))
		if strings.Contains(full, "…") || strings.Contains(full, "...") {
			t.Fatal(full)
		}
	}
}
func TestApprovalBackAndExactPersistentScope(t *testing.T) {
	p := NewApproval()
	req := &perm.PermissionRequest{ToolName: "Bash", AllowRules: []string{"Bash(ls)", "Bash(pwd)"}}
	p.Show(req, 100, 24)
	approvalKey(&p, "3")
	approvalKey(&p, "3")
	view := ansi.Strip(p.Render())
	for _, text := range []string{"Bash(ls)", "Bash(pwd)", ".san/settings.local.json", "Esc back"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %q: %s", text, view)
		}
	}
	if approvalKey(&p, "y") != nil || p.screen != 2 {
		t.Fatal("Y grants scope")
	}
	if approvalKey(&p, "enter") != nil || p.screen != 1 {
		t.Fatal("confirmation must default Back")
	}
	approvalKey(&p, "2")
	if approvalKey(&p, "esc") != nil || p.screen != 1 {
		t.Fatal("Esc must return one level")
	}
	if approvalKey(&p, "esc") != nil || p.screen != 0 {
		t.Fatal("Esc must return to base")
	}
	if r := approvalKey(&p, "esc"); r == nil || r.Approved || r.Request != req {
		t.Fatal(r)
	}
}

func TestApprovalCommandUsesSharedInsetSurface(t *testing.T) {
	command := "  echo " + strings.Repeat("token-with-spaces ", 30)
	preview := newApprovalBashPreview(&perm.BashMetadata{Command: command})
	for _, width := range []int{32, 40, 80} {
		full := preview.render(width)
		if full != strings.TrimSuffix(kit.RenderCommandBlock(command, width), "\n") {
			t.Fatalf("width %d differs from the shared lossless command block: %q", width, full)
		}
		for _, row := range strings.Split(ansi.Strip(full), "\n") {
			if ansi.StringWidth(row) != width-1 || !strings.HasPrefix(row, "  ") {
				t.Fatalf("width %d has ragged or unindented preview: %q", width, row)
			}
		}
	}
}

func TestApprovalDiffRemainsNumberedAndScrollable(t *testing.T) {
	p := NewApproval()
	lines := []perm.DiffLine{{Type: perm.DiffLineRemoved, Content: "old-value", OldLineNo: 7}, {Type: perm.DiffLineAdded, Content: "new-value", NewLineNo: 7}}
	for i := 8; i < 40; i++ {
		lines = append(lines, perm.DiffLine{Type: perm.DiffLineAdded, Content: "tail-value", NewLineNo: i})
	}
	p.Show(&perm.PermissionRequest{ToolName: "Edit", FilePath: "example.go", DiffMeta: &perm.DiffMetadata{Lines: lines, AddedCount: 33, RemovedCount: 1}}, 40, 10)
	first := ansi.Strip(p.Render())
	for _, text := range []string{"old-value", "new-value", "7"} {
		if !strings.Contains(first, text) {
			t.Fatal(first)
		}
	}
	seen := first
	for i := 0; i < 30; i++ {
		p.TogglePreview()
		seen += ansi.Strip(p.Render())
	}
	if !strings.Contains(seen, "39") || !strings.Contains(seen, "tail-value") {
		t.Fatal("diff tail not reachable")
	}
}

func TestBuildApprovalOptionsMatchesRows(t *testing.T) {
	req := &perm.PermissionRequest{ToolName: "Edit"}
	labels := BuildApprovalOptions(req)
	rows := buildApprovalOptionRows(req)
	if len(labels) != len(rows) {
		t.Fatal(labels)
	}
	for i := range rows {
		if labels[i] != rows[i].Label {
			t.Fatal(labels)
		}
	}
}
