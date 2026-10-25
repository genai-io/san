package input

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// ApprovalModel manages the permission request UI with Claude Code style.
type ApprovalModel struct {
	active       bool
	request      *perm.PermissionRequest
	diffPreview  *approvalDiffPreview
	bashPreview  *approvalBashPreview
	skillPreview *approvalSkillPreview
	agentPreview *approvalAgentPreview
	width        int
	selectedIdx  int
	height       int
	screen       int // 0: current call, 1: more permissions, 2: scope confirmation
	pending      approvalOption
	scroll       int
	previewKey   approvalPreviewKey
	previewLines []string
}

// Preview content is fixed for a shown request; only layout and colors vary.
type approvalPreviewKey struct {
	width, screen          int
	persist, compact, dark bool
	profile                colorprofile.Profile
}

// NewApproval creates a new ApprovalModel instance
func NewApproval() ApprovalModel {
	return ApprovalModel{
		selectedIdx: 0,
	}
}

func (p *ApprovalModel) setRequest(req *perm.PermissionRequest, width int) {
	p.active = true
	p.request = req
	p.width = width
	p.selectedIdx = 0
	p.screen, p.scroll = 0, 0
	p.pending = approvalOption{}
	p.previewLines = nil

	if req.DiffMeta != nil {
		p.diffPreview = newApprovalDiffPreview(req.DiffMeta, req.FilePath)
	} else {
		p.diffPreview = nil
	}

	if req.BashMeta != nil {
		p.bashPreview = newApprovalBashPreview(req.BashMeta)
	} else {
		p.bashPreview = nil
	}

	if req.SkillMeta != nil {
		p.skillPreview = newApprovalSkillPreview(req.SkillMeta)
	} else {
		p.skillPreview = nil
	}

	if req.AgentMeta != nil {
		p.agentPreview = newApprovalAgentPreview(req.AgentMeta)
	} else {
		p.agentPreview = nil
	}
}

// Show displays the permission prompt with the given request.
func (p *ApprovalModel) Show(req *perm.PermissionRequest, width, height int) {
	p.setRequest(req, width)
	p.Resize(width, height)
}

// Resize updates the available approval panel budget.
func (p *ApprovalModel) Resize(width, height int) {
	p.width, p.height = max(width, 1), max(height, 1)
}

// Hide hides the permission prompt
func (p *ApprovalModel) Hide() {
	p.active = false
	p.request = nil
	p.diffPreview = nil
	p.bashPreview = nil
	p.skillPreview = nil
	p.agentPreview = nil
	p.previewLines = nil
}

// IsActive returns whether the prompt is visible
func (p *ApprovalModel) IsActive() bool {
	return p.active
}

// TogglePreview advances the scrollable preview by one page.
func (p *ApprovalModel) previewPageSize() int {
	return max(1, p.height-2-len(p.options())-2)
}

func (p *ApprovalModel) TogglePreview() {
	p.scroll += p.previewPageSize()
}

// ApprovalResponseMsg is sent when the user responds to a permission request
type ApprovalResponseMsg struct {
	Approved bool
	AllowAll bool
	Persist  bool
	Request  *perm.PermissionRequest
}

// HandleKeypress consumes a keypress while the modal is open. When the user
// makes a decision it emits the ApprovalResponseMsg as a command, so the root
// model handles the result in Update like any other message. That keeps the
// modal a uniform overlayPanel rather than a special case in the key router.
func (p *ApprovalModel) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
	cmd, resp := p.handleKeypress(msg)
	if resp == nil {
		return cmd
	}
	out := *resp
	return tea.Batch(cmd, func() tea.Msg { return out })
}

// handleKeypress advances modal state for a keypress, returning a response
// once the user has made a decision.
func (p *ApprovalModel) handleKeypress(msg tea.KeyMsg) (tea.Cmd, *ApprovalResponseMsg) {
	if !p.active || msg.Key().IsRepeat {
		return nil, nil
	}
	rows := p.options()
	s := msg.String()
	switch s {
	case "up", "ctrl+p", "shift+tab":
		p.selectedIdx = max(0, p.selectedIdx-1)
	case "down", "ctrl+n", "tab":
		p.selectedIdx = min(len(rows)-1, p.selectedIdx+1)
	case "pgdown", "ctrl+o":
		p.scroll += p.previewPageSize()
	case "pgup":
		p.scroll = max(0, p.scroll-p.previewPageSize())
	case "esc", "ctrl+c":
		if p.screen > 0 {
			p.screen--
			p.selectedIdx = 0
			p.scroll = 0
		} else {
			return p.respondAt(rows, 1)
		}
	case "enter", "space", " ":
		return p.activate(rows, p.selectedIdx)
	default:
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			return p.activate(rows, int(s[0]-'1'))
		}
		if p.screen == 0 {
			switch s {
			case "y", "Y":
				return p.respondAt(rows, 0)
			case "n", "N":
				return p.respondAt(rows, 1)
			}
		}
	}
	return nil, nil
}

func (p *ApprovalModel) options() []approvalOption {
	switch p.screen {
	case 1:
		rows := buildApprovalOptionRows(p.request)
		return append([]approvalOption{{Label: "Back"}}, rows[1:len(rows)-1]...)
	case 2:
		return []approvalOption{{Label: "Back"}, {Label: "Confirm permission", Approved: true, AllowAll: p.pending.AllowAll, Persist: p.pending.Persist}}
	default:
		rows := []approvalOption{{Label: "Allow once", Approved: true}, {Label: "Deny"}}
		if p.request != nil && !p.request.OnceOnly {
			rows = append(rows, approvalOption{Label: "More permissions…"})
		}
		return rows
	}
}

func (p *ApprovalModel) activate(rows []approvalOption, i int) (tea.Cmd, *ApprovalResponseMsg) {
	if i < 0 || i >= len(rows) {
		return nil, nil
	}
	if p.screen == 0 && i == 2 {
		p.screen = 1
		p.selectedIdx = 0
		p.scroll = 0
		return nil, nil
	}
	if p.screen > 0 && i == 0 {
		p.screen--
		p.selectedIdx = 0
		p.scroll = 0
		return nil, nil
	}
	if p.screen == 1 {
		p.pending = rows[i]
		p.screen = 2
		p.selectedIdx = 0
		p.scroll = 0
		return nil, nil
	}
	return p.respondAt(rows, i)
}

func (p *ApprovalModel) respondAt(options []approvalOption, i int) (tea.Cmd, *ApprovalResponseMsg) {
	if i < 0 || i >= len(options) {
		return nil, nil
	}
	opt := options[i]
	req := p.request
	p.Hide()
	return nil, &ApprovalResponseMsg{
		Approved: opt.Approved, AllowAll: opt.AllowAll, Persist: opt.Persist, Request: req,
	}
}

// --- Approval style helpers ---

func approvalSeparatorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Separator)
}

func approvalQuestionStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
}

func approvalSelectedStyle() lipgloss.Style {
	// Neutral bright+bold for the focused option; the teal FocusBar carries the
	// "selected" signal. Green is reserved for genuine success/confirm states.
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright).Bold(true)
}

func approvalUnselectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
}

func approvalHintStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted).Italic(true)
}

func approvalFooterStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
}

func approvalTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
}

func (p *ApprovalModel) wrappedPreview(width int, compact bool) []string {
	key := approvalPreviewKey{width: width, screen: p.screen, persist: p.pending.Persist,
		compact: compact, dark: kit.IsDarkBackground(), profile: lipgloss.Writer.Profile}
	if p.previewLines != nil && p.previewKey == key {
		return p.previewLines
	}
	var preview string
	if p.screen == 2 {
		if p.pending.Persist {
			preview = "Save exact allow rules: " + strings.Join(p.request.AllowRules, ", ") + "\nProject-local .san/settings.local.json"
		} else {
			preview = "Allow future calls to " + p.request.ToolName + " for this session, beyond this command. This does not allow all tools. Safety checks and explicit ask rules still apply."
			preview = ansi.Wrap(preview, width, " ")
		}
	} else if p.diffPreview != nil {
		p.diffPreview.expanded = true
		preview = p.diffPreview.render(width)
	} else if p.bashPreview != nil {
		preview = p.bashPreview.render(width)
	} else if p.skillPreview != nil {
		preview = p.skillPreview.render(width)
	} else if p.agentPreview != nil {
		preview = p.agentPreview.render(width)
	} else {
		preview = p.request.Description
	}
	// Keep the compact header, but make the complete requester/title available
	// in the scrollable region rather than losing safety-relevant identity.
	if ansi.StringWidth("● "+p.getTitle()) > width {
		preview = p.getTitle() + "\n" + preview
	}
	if compact {
		preview += "\n" + p.request.PermissionMode + " · cwd: " + strconv.QuoteToGraphic(p.request.WorkingDirectory)
	}
	p.previewKey = key
	p.previewLines = strings.Split(strings.TrimRight(ansi.Hardwrap(preview, width, true), "\n"), "\n")
	return p.previewLines
}

func (p *ApprovalModel) renderInline() string {
	if !p.active || p.request == nil {
		return ""
	}
	w := max(1, p.width)
	h := p.height
	if h <= 0 {
		h = 24
	}
	header := []string{approvalTitleStyle().Render(kit.TruncateText("● "+p.getTitle(), w)), approvalHintStyle().Render(kit.TruncateText(p.request.PermissionMode+" · cwd: "+strconv.QuoteToGraphic(p.request.WorkingDirectory), w))}
	compact := h < len(header)+len(p.options())+2
	if compact {
		// On very short panels, keep the operation inspectable before cwd chrome.
		header = header[:1]
	}
	lines := p.wrappedPreview(w, compact)
	var menu []string
	for i, row := range p.options() {
		prefix := "  "
		if i == p.selectedIdx {
			prefix = "> "
		}
		label := kit.TruncateText(fmt.Sprintf("%s%d. %s", prefix, i+1, row.Label), w)
		if i == p.selectedIdx {
			label = approvalSelectedStyle().Render(label)
		} else {
			label = approvalUnselectedStyle().Render(label)
		}
		menu = append(menu, label)
	}
	// Reserve the decision rows and hint first. A scroll notice consumes a
	// row only when needed; never force a preview row past the panel budget.
	budget := max(0, h-len(header)-len(menu)-1)
	// A scroll label must never consume the last inspectable operation row.
	scrollNotice := len(lines) > budget && budget > 1
	if scrollNotice {
		budget--
	}
	p.scroll = min(p.scroll, max(0, len(lines)-max(1, budget)))
	end := min(len(lines), p.scroll+budget)
	var hint string
	if p.screen > 0 {
		hint = "Enter confirm · Esc back"
	} else {
		hint = "Enter · Y once · N/Esc deny"
	}
	if !scrollNotice && len(lines) > budget {
		if p.screen == 0 {
			hint = "Y once · Esc deny · PgDn"
		} else {
			hint = "Enter · Esc back · PgDn"
		}
	}
	out := append(header, lines[p.scroll:end]...)
	out = append(out, menu...)
	out = append(out, kit.TruncateText(hint, w))
	if scrollNotice {
		out = append(out, kit.TruncateText(fmt.Sprintf("PgUp/PgDown/Ctrl+O scroll %d/%d", p.scroll+1, len(lines)), w))
	}
	return strings.Join(out, "\n")
}

func (p *ApprovalModel) getTitle() string {
	var title string
	switch p.request.ToolName {
	case "Edit":
		title = "Edit file"
	case "Write":
		title = "Write to file"
	case tool.ToolBash, tool.ToolPowerShell:
		title = p.request.ToolName + " command"
	case tool.ToolSkill:
		title = "Load skill"
	case tool.ToolAgent:
		title = "Spawn agent"
	case tool.ToolSendMessage:
		// "Message @web in group shop: <body>" — the body is previewed below.
		title, _, _ = strings.Cut(p.request.Description, ": ")
	default:
		title = p.request.ToolName
	}

	if p.request.CallerAgent != "" {
		title = "@" + p.request.CallerAgent + " · " + title
	}
	return title
}

// approvalOption describes a permission outcome or navigation row.
type approvalOption struct {
	Label    string
	Hint     string
	Approved bool
	AllowAll bool
	Persist  bool
}

// buildApprovalOptionRows lists eligible outcomes for tracing and the More menu.
// Session and persistent outcomes require a separate scope confirmation in the UI.
func buildApprovalOptionRows(req *perm.PermissionRequest) []approvalOption {
	rows := []approvalOption{{Label: "Allow once", Approved: true}}
	if req != nil && !req.OnceOnly {
		rows = append(rows, approvalOption{Label: "Allow " + req.ToolName + " for this session…", Approved: true, AllowAll: true})
	}
	if req != nil && !req.OnceOnly && len(req.AllowRules) > 0 {
		rows = append(rows, approvalOption{Label: alwaysAllowLabel(req.AllowRules), Approved: true, Persist: true})
	}
	return append(rows, approvalOption{Label: "Deny"})
}

// BuildApprovalOptions returns eligible outcome labels for the trace writer.
// Navigation rows and the scope-confirmation screen are intentionally omitted.
func BuildApprovalOptions(req *perm.PermissionRequest) []string {
	rows := buildApprovalOptionRows(req)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Label
	}
	return out
}

// alwaysAllowLabel names the rule the option saves, so what is granted is
// what the user reads.
func alwaysAllowLabel(rules []string) string {
	return "Always allow " + strings.Join(rules, ", ")
}

// Render renders the permission prompt (calls renderInline)
func (p *ApprovalModel) Render() string {
	return p.renderInline()
}

// --- Agent preview ---

type approvalAgentPreview struct {
	agentMeta *perm.AgentMetadata
}

// renderMessagePreview shows what a SendMessage would send, wrapped and
// capped, so the person approves the words and not just the recipient.
func renderMessagePreview(body string, width int) string {
	const maxLines = 8
	wrapped := lipgloss.NewStyle().Width(max(width-4, 20)).Render(strings.TrimSpace(body))
	lines := strings.Split(wrapped, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("… %d more lines", len(lines)-maxLines))
	}
	dim := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString("   " + dim.Render(strings.TrimRight(line, " ")) + "\n")
	}
	return sb.String()
}

func newApprovalAgentPreview(meta *perm.AgentMetadata) *approvalAgentPreview {
	return &approvalAgentPreview{agentMeta: meta}
}

func (p *approvalAgentPreview) render(width int) string {
	if p.agentMeta == nil {
		return ""
	}

	var sb strings.Builder

	nameStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	labelStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	modeStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)

	sb.WriteString("   ")
	sb.WriteString(nameStyle.Render(p.agentMeta.AgentName))
	if p.agentMeta.Background {
		sb.WriteString(" ")
		sb.WriteString(modeStyle.Render("[background]"))
	}
	sb.WriteString("\n")

	if p.agentMeta.Description != "" {
		sb.WriteString("   ")
		sb.WriteString(dimStyle.Render(p.agentMeta.Description))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	if p.agentMeta.Model != "" {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Model: "))
		sb.WriteString(dimStyle.Render(p.agentMeta.Model))
		sb.WriteString("\n")
	}

	sb.WriteString("   ")
	sb.WriteString(labelStyle.Render("Mode: "))
	modeLabel := approvalFormatPermissionMode(p.agentMeta.PermissionMode)
	sb.WriteString(dimStyle.Render(modeLabel))
	sb.WriteString("\n")

	if len(p.agentMeta.Tools) > 0 {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Tools: "))
		sb.WriteString(dimStyle.Render(strings.Join(p.agentMeta.Tools, ", ")))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	sb.WriteString("   ")
	sb.WriteString(labelStyle.Render("Task:"))
	sb.WriteString("\n")

	// Keep the existing prose bound, but let the panel scroll through every
	// wrapped line rather than truncating each source line independently.
	prompt := kit.TruncateText(p.agentMeta.Prompt, 500)
	for _, line := range strings.Split(prompt, "\n") {
		sb.WriteString("   ")
		sb.WriteString(dimStyle.Render(line))
		sb.WriteString("\n")
	}

	// Wrap inside the indentation budget so the outer panel hardwrap never
	// creates unindented continuation lines. ANSI widths include CJK cells.
	indent := strings.Repeat(" ", min(3, max(0, width-2)))
	budget := max(1, width-len(indent))
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n") {
		line = strings.TrimPrefix(line, "   ")
		for _, wrapped := range strings.Split(ansi.Hardwrap(line, budget, true), "\n") {
			if wrapped == "" {
				lines = append(lines, "")
			} else {
				lines = append(lines, indent+wrapped)
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func approvalFormatPermissionMode(mode string) string {
	switch mode {
	case "explore":
		return "Explore mode"
	case "edit":
		return "Edit mode"
	case "default":
		return "Default mode"
	default:
		return mode
	}
}

// --- Skill preview ---

type approvalSkillPreview struct {
	skillMeta *perm.SkillMetadata
}

func newApprovalSkillPreview(meta *perm.SkillMetadata) *approvalSkillPreview {
	return &approvalSkillPreview{skillMeta: meta}
}

func (p *approvalSkillPreview) render(width int) string {
	if p.skillMeta == nil {
		return ""
	}

	var sb strings.Builder

	nameStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Primary).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	labelStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)

	sb.WriteString("   ")
	sb.WriteString(nameStyle.Render(p.skillMeta.SkillName))
	sb.WriteString("\n")

	if p.skillMeta.Description != "" {
		sb.WriteString("   ")
		sb.WriteString(dimStyle.Render(p.skillMeta.Description))
		sb.WriteString("\n")
	}

	if p.skillMeta.Args != "" {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Args: "))
		sb.WriteString(dimStyle.Render(p.skillMeta.Args))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	var resources []string
	if p.skillMeta.ScriptCount > 0 {
		if p.skillMeta.ScriptCount == 1 {
			resources = append(resources, "1 script")
		} else {
			resources = append(resources, fmt.Sprintf("%d scripts", p.skillMeta.ScriptCount))
		}
	}
	if p.skillMeta.RefCount > 0 {
		if p.skillMeta.RefCount == 1 {
			resources = append(resources, "1 reference")
		} else {
			resources = append(resources, fmt.Sprintf("%d references", p.skillMeta.RefCount))
		}
	}

	if len(resources) > 0 {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Resources: "))
		sb.WriteString(dimStyle.Render(strings.Join(resources, ", ")))
		sb.WriteString("\n")
	}

	if len(p.skillMeta.Scripts) > 0 && len(p.skillMeta.Scripts) <= 5 {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Scripts: "))
		sb.WriteString(dimStyle.Render(strings.Join(p.skillMeta.Scripts, ", ")))
		sb.WriteString("\n")
	}

	if len(p.skillMeta.References) > 0 && len(p.skillMeta.References) <= 5 {
		sb.WriteString("   ")
		sb.WriteString(labelStyle.Render("Refs: "))
		sb.WriteString(dimStyle.Render(strings.Join(p.skillMeta.References, ", ")))
		sb.WriteString("\n")
	}

	return sb.String()
}
