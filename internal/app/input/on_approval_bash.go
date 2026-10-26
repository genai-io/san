package input

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

const approvalDefaultMaxCommandLines = 20

type approvalBashPreview struct {
	bashMeta   *perm.BashMetadata
	expanded   bool
	maxVisible int
}

func newApprovalBashPreview(meta *perm.BashMetadata) *approvalBashPreview {
	return &approvalBashPreview{bashMeta: meta, maxVisible: approvalDefaultMaxCommandLines}
}
func (b *approvalBashPreview) toggleExpand() { b.expanded = !b.expanded }
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
func (b *approvalBashPreview) needsExpand() bool {
	return b.bashMeta != nil && len(strings.Split(b.bashMeta.Command, "\n")) > b.maxVisible
}
