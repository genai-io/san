package input

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
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
	rows = append(rows, strings.TrimSuffix(kit.RenderCommandBlock(b.bashMeta.Command, width), "\n"))
	if b.bashMeta.Description != "" {
		rows = append(rows, ansi.Hardwrap(b.bashMeta.Description, max(1, width), true))
	}
	return strings.Join(rows, "\n")
}
