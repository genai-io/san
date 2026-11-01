package conv

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAssistantCJKParagraphUsesAvailableWidth(t *testing.T) {
	const text = "梳理时发现几条提交路径的 Hook 行为不同，不能直接把 Hook 塞进所有消息入口，否则会改变 TUI 行为。另外，临时工作区被外部清理了，目前没有产生代码修改。我会换用新的隔离目录继续，保留现有路径差异，并用测试固定这些边界。"
	for _, width := range []int{140, 160, 180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			md := NewMDRenderer(width)
			for name, rendered := range map[string]string{
				"message":    RenderAssistantMessage(AssistantParams{Content: text, Width: width, MDRenderer: md}),
				"scrollback": RenderCommittedContentBlock(text, true, md),
			} {
				plain := ansi.Strip(rendered)
				first, _, _ := strings.Cut(plain, "\n")
				if !strings.HasPrefix(first, "● ") || ansi.StringWidth(strings.TrimRight(first, " ")) < width-8 {
					t.Errorf("%s leaves unused paragraph width: %q", name, first)
				}
				var recovered strings.Builder
				for i, line := range strings.Split(strings.TrimRight(plain, "\n"), "\n") {
					if ansi.StringWidth(line) > width {
						t.Errorf("%s exceeds %d columns: %q", name, width, line)
					}
					if i == 0 {
						line = strings.TrimPrefix(line, "● ")
					}
					recovered.WriteString(strings.TrimSpace(line))
				}
				if strings.ReplaceAll(recovered.String(), " ", "") != strings.ReplaceAll(text, " ", "") {
					t.Errorf("%s lost or changed paragraph text: %q", name, recovered.String())
				}
			}
		})
	}
}
