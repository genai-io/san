package conv

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

// Glamour's blank rows carry spaces and style codes, so a margin row and the
// next block's stacked into two blank rows (seen under a CJK heading).
func TestRenderKeepsOneBlankRowBetweenBlocks(t *testing.T) {
	out, err := NewMDRenderer(100).Render("还在跑。\n\n## 现状\n\n- 命令：`sleep 15`\n- 状态：仍在运行\n\n之后")
	if err != nil {
		t.Fatal(err)
	}
	prevBlank := false
	for _, line := range strings.Split(out, "\n") {
		blank := strings.TrimSpace(xansi.Strip(line)) == ""
		if blank && prevBlank {
			t.Fatalf("two blank rows in a row:\n%q", out)
		}
		prevBlank = blank
	}
}
