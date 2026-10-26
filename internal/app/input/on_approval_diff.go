package input

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/perm"
)

const (
	approvalDefaultMaxVisibleLines = 20
)

type approvalDiffPreview struct {
	diffMeta   *perm.DiffMetadata
	filePath   string
	expanded   bool
	maxVisible int
}

func newApprovalDiffPreview(diffMeta *perm.DiffMetadata, filePath string) *approvalDiffPreview {
	return &approvalDiffPreview{
		diffMeta:   diffMeta,
		filePath:   filePath,
		expanded:   false,
		maxVisible: approvalDefaultMaxVisibleLines,
	}
}

func (d *approvalDiffPreview) toggleExpand() {
	d.expanded = !d.expanded
}

func approvalDiffAddedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)
}

func approvalDiffRemovedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)
}

func approvalDiffContextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
}

func approvalDiffLineNoStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(kit.CurrentTheme.TextDisabled).
		Width(5).
		Align(lipgloss.Right)
}

func approvalDiffMoreStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Italic(true)
}

func approvalDiffHeaderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
}

func (d *approvalDiffPreview) render(width int) string {
	if d.diffMeta == nil || len(d.diffMeta.Lines) == 0 {
		return approvalDiffContextStyle().Render("  (no changes)")
	}

	if d.diffMeta.IsNewFile || d.diffMeta.PreviewMode {
		return d.renderNewFilePreview(width)
	}

	return d.renderUnifiedDiff(width)
}

func (d *approvalDiffPreview) renderFileHeader(label string) string {
	name := d.filePath
	if name != "" {
		name = filepath.Base(name)
	}
	if name == "" {
		name = "file"
	}
	return approvalDiffHeaderStyle().Render(fmt.Sprintf(" %s  %s", name, label))
}

func (d *approvalDiffPreview) renderNewFilePreview(width int) string {
	maxVisible := d.maxVisible
	if d.expanded {
		maxVisible = 0
	}
	// Write's real preview metadata stores file contents as context rows. A new
	// file is entirely additions; derive display rows without mutating the request.
	lines := d.diffMeta.Lines
	if d.diffMeta.IsNewFile {
		lines = append([]perm.DiffLine(nil), lines...)
		for i := range lines {
			if lines[i].Type == perm.DiffLineContext {
				lines[i].Type = perm.DiffLineAdded
			}
		}
	}
	block, hidden := conv.RenderFileDiff(lines, max(1, width), maxVisible)
	out := d.renderFileHeader("(new file)") + "\n" + block
	if hidden > 0 {
		out += approvalDiffMoreStyle().Render(fmt.Sprintf("... %d more lines (Ctrl+O to expand)", hidden)) + "\n"
	}
	return out
}

func (d *approvalDiffPreview) renderUnifiedDiff(width int) string {
	var sb strings.Builder

	added := approvalDiffAddedStyle().Render(fmt.Sprintf("+%d", d.diffMeta.AddedCount))
	removed := approvalDiffRemovedStyle().Render(fmt.Sprintf("-%d", d.diffMeta.RemovedCount))
	sb.WriteString(d.renderFileHeader(fmt.Sprintf("%s %s", added, removed)))
	sb.WriteString("\n")

	maxVisible := d.maxVisible
	if d.expanded {
		maxVisible = 0
	}
	block, hiddenCount := conv.RenderFileDiff(d.diffMeta.Lines, width, maxVisible)
	sb.WriteString(block)

	if hiddenCount > 0 {
		msg := fmt.Sprintf("... %d more lines (Ctrl+O to expand)", hiddenCount)
		sb.WriteString(approvalDiffMoreStyle().Render(msg))
		sb.WriteString("\n")
	}

	return sb.String()
}
