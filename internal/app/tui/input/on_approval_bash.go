package input

import (
	"strings"

	"github.com/genai-io/san/internal/app/tui/transcript"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/tool/perm"
)

type approvalBashPreview struct {
	bashMeta *perm.BashMetadata
}

func newApprovalBashPreview(meta *perm.BashMetadata) *approvalBashPreview {
	return &approvalBashPreview{bashMeta: meta}
}
func (b *approvalBashPreview) render(width int) string {
	if b.bashMeta == nil {
		return ""
	}
	var rows []string
	if b.bashMeta.RunBackground {
		rows = append(rows, "[background]")
	}
	rows = append(rows, strings.TrimSuffix(widgets.RenderCommandBlock(b.bashMeta.Command, width), "\n"))
	if b.bashMeta.Description != "" {
		rows = append(rows, transcript.WrapProse(b.bashMeta.Description, max(1, width)))
	}
	return strings.Join(rows, "\n")
}
