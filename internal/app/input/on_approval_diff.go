package input

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

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

func approvalDiffAddedBgStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success).Background(kit.CurrentTheme.SuccessBg)
}

func approvalDiffRemovedBgStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error).Background(kit.CurrentTheme.ErrorBg)
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
	var sb strings.Builder

	sb.WriteString(d.renderFileHeader("(new file)"))
	sb.WriteString("\n")
	sep := strings.Repeat("─", width)
	sb.WriteString(approvalDiffContextStyle().Render(sep))
	sb.WriteString("\n")

	lines := d.diffMeta.Lines
	showCount := len(lines)
	truncated := false

	if !d.expanded && showCount > d.maxVisible {
		showCount = d.maxVisible
		truncated = true
	}

	for i := 0; i < showCount; i++ {
		line := lines[i]
		if line.Type == perm.DiffLineHunk || line.Type == perm.DiffLineMetadata {
			continue
		}
		lineNo := fmt.Sprintf("%4d", line.NewLineNo)
		sb.WriteString(approvalDiffLineNoStyle().Render(lineNo))
		sb.WriteString(approvalDiffContextStyle().Render(" │ "))
		sb.WriteString(approvalDiffAddedStyle().Render(line.Content))
		sb.WriteString("\n")
	}

	if truncated {
		remaining := len(lines) - d.maxVisible
		msg := fmt.Sprintf("... %d more lines (Ctrl+O to expand)", remaining)
		sb.WriteString(approvalDiffMoreStyle().Render(msg))
		sb.WriteString("\n")
	}

	return sb.String()
}

func (d *approvalDiffPreview) renderUnifiedDiff(width int) string {
	var sb strings.Builder

	added := approvalDiffAddedStyle().Render(fmt.Sprintf("+%d", d.diffMeta.AddedCount))
	removed := approvalDiffRemovedStyle().Render(fmt.Sprintf("-%d", d.diffMeta.RemovedCount))
	sb.WriteString(d.renderFileHeader(fmt.Sprintf("%s %s", added, removed)))
	sb.WriteString("\n")

	const prefix = 7
	contentWidth := width - prefix
	if contentWidth < 8 {
		contentWidth = 8
	}

	lines := d.diffMeta.Lines
	showCount := len(lines)
	truncated := false
	if !d.expanded && showCount > d.maxVisible {
		showCount = d.maxVisible
		truncated = true
	}

	removedBgStyle := approvalDiffRemovedBgStyle()
	addedBgStyle := approvalDiffAddedBgStyle()
	contextStyle := approvalDiffContextStyle()

	for i := 0; i < showCount; i++ {
		line := lines[i]

		switch line.Type {
		case perm.DiffLineHunk, perm.DiffLineMetadata:
			// Skip

		case perm.DiffLineContext:
			no := fmt.Sprintf("%4d", line.OldLineNo)
			content := approvalTruncateContent(line.Content, contentWidth)
			sb.WriteString(contextStyle.Render(no + "   " + content))
			sb.WriteString("\n")

		case perm.DiffLineRemoved:
			no := fmt.Sprintf("%4d", line.OldLineNo)
			content := approvalTruncateOrPad(approvalTruncateContent(line.Content, contentWidth), contentWidth)
			sb.WriteString(removedBgStyle.Render(approvalTruncateOrPad(no+" - "+content, width)))
			sb.WriteString("\n")

		case perm.DiffLineAdded:
			no := fmt.Sprintf("%4d", line.NewLineNo)
			content := approvalTruncateOrPad(approvalTruncateContent(line.Content, contentWidth), contentWidth)
			sb.WriteString(addedBgStyle.Render(approvalTruncateOrPad(no+" + "+content, width)))
			sb.WriteString("\n")
		}
	}

	if truncated {
		remaining := len(lines) - d.maxVisible
		msg := fmt.Sprintf("... %d more lines (Ctrl+O to expand)", remaining)
		sb.WriteString(approvalDiffMoreStyle().Render(msg))
		sb.WriteString("\n")
	}

	return sb.String()
}

func approvalTruncateContent(content string, width int) string {
	displayWidth := runewidth.StringWidth(content)
	if displayWidth > width {
		if width > 3 {
			return runewidth.Truncate(content, width-3, "...")
		}
		return runewidth.Truncate(content, width, "")
	}
	return content
}

func approvalTruncateOrPad(content string, width int) string {
	displayWidth := runewidth.StringWidth(content)
	if displayWidth > width {
		if width > 3 {
			return runewidth.Truncate(content, width-3, "...")
		}
		return runewidth.Truncate(content, width, "")
	}
	padding := width - displayWidth
	return content + strings.Repeat(" ", padding)
}
