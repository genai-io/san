package input

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestAgentPreviewWrappedIdentityAndProse(t *testing.T) {
	for _, width := range []int{32, 40} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			meta := &perm.AgentMetadata{AgentName: strings.Repeat("Review工作", 8), Description: strings.Repeat("Detailed描述", 8), Tools: []string{strings.Repeat("LongTool工具", 8)}, Model: "model", PermissionMode: "default", Prompt: strings.Repeat("Inspect carefully 工作 then report findings. ", 6) + "FINAL evidence", Background: true}
			rendered := ansi.Strip(newApprovalAgentPreview(meta).render(width))
			var joined strings.Builder
			for _, line := range strings.Split(rendered, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("overflow: %q", line)
				}
				if line != "" && !strings.HasPrefix(line, "   ") {
					t.Fatalf("lost indent: %q", line)
				}
				joined.WriteString(strings.TrimPrefix(line, "   "))
			}
			for _, want := range []string{meta.AgentName, meta.Description, meta.Tools[0], meta.Prompt} {
				if !strings.Contains(joined.String(), want) {
					t.Fatalf("lost prose %q in %q", want, joined.String())
				}
			}
			p := NewApproval()
			p.Show(&perm.PermissionRequest{ToolName: "Agent", AgentMeta: meta}, width, 12)
			if !strings.HasPrefix(ansi.Strip(p.renderInline()), "● ") {
				t.Fatal("missing round marker")
			}
			for i := 0; i < 20; i++ {
				for _, line := range strings.Split(ansi.Strip(p.renderInline()), "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("panel overflow: %q", line)
					}
				}
				p.TogglePreview()
			}
		})
	}
}
