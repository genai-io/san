package input

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/gen-code/internal/app/kit"
	coreskill "github.com/genai-io/gen-code/internal/skill"
)

type skillItem struct {
	Name        string
	Namespace   string
	Description string
	Hint        string
	State       coreskill.SkillState
	Scope       coreskill.SkillScope
}

func (s *skillItem) FullName() string {
	if s.Namespace != "" {
		return s.Namespace + ":" + s.Name
	}
	return s.Name
}

type SkillCycleMsg struct {
	SkillName string
	NewState  coreskill.SkillState
}

// skillTab identifies a category tab in the skill selector.
type skillTab int

const (
	skillTabProject skillTab = iota
	skillTabUser
)

func (t skillTab) String() string {
	if t == skillTabUser {
		return "User"
	}
	return "Project"
}

type SkillSelector struct {
	registry       *coreskill.Registry
	active         bool
	skills         []skillItem
	filteredSkills []skillItem
	nav            kit.ListNav
	width          int
	height         int
	activeTab      skillTab
}

type SkillState struct {
	Selector            SkillSelector
	PendingInstructions string
	PendingArgs         string
	ActiveInvocation    string
}

// ConsumeInvocation extracts the pending skill invocation, activating any
// pending instructions and clearing pending state.
// Returns (displayMsg, fullMsg): displayMsg is shown in chat UI,
// fullMsg includes skill instructions and is sent to the LLM.
func (s *SkillState) ConsumeInvocation() (displayMsg, fullMsg string) {
	displayMsg = s.PendingArgs
	if displayMsg == "" {
		displayMsg = "Execute the skill."
	}
	fullMsg = displayMsg
	if s.PendingInstructions != "" {
		s.ActiveInvocation = s.PendingInstructions
		fullMsg = s.PendingInstructions + "\n\n" + displayMsg
		s.PendingInstructions = ""
	}
	s.PendingArgs = ""
	return displayMsg, fullMsg
}

// ClearPending resets pending skill state without activating.
func (s *SkillState) ClearPending() {
	s.PendingInstructions = ""
	s.PendingArgs = ""
}

func NewSkillSelector(reg *coreskill.Registry) SkillSelector {
	return SkillSelector{
		registry:  reg,
		active:    false,
		skills:    []skillItem{},
		nav:       kit.ListNav{MaxVisible: 10},
		activeTab: skillTabProject,
	}
}

// EnterSelect activates the selector and loads skills with their states.
func (s *SkillSelector) EnterSelect(width, height int) error {
	if s.registry == nil {
		return fmt.Errorf("skill registry not initialized")
	}

	allSkills := s.registry.List()
	// Pre-load both stores so each tab shows the correct enabled state.
	statesByLevel := map[bool]map[string]coreskill.SkillState{
		false: s.registry.GetStatesAt(false),
		true:  s.registry.GetStatesAt(true),
	}

	s.skills = make([]skillItem, 0, len(allSkills))
	for _, sk := range allSkills {
		userLevel := scopeIsUser(sk.Scope)
		state := sk.State
		if levelState, ok := statesByLevel[userLevel][sk.FullName()]; ok {
			state = levelState
		}
		s.skills = append(s.skills, skillItem{
			Name:        sk.Name,
			Namespace:   sk.Namespace,
			Description: sk.Description,
			Hint:        sk.ArgumentHint,
			State:       state,
			Scope:       sk.Scope,
		})
	}

	s.active = true
	s.width = width
	s.height = height
	s.nav.Reset()
	s.activeTab = s.firstNonEmptyTab()
	s.applyFilters()
	return nil
}

func scopeIsUser(scope coreskill.SkillScope) bool {
	switch scope {
	case coreskill.ScopeClaudeUser, coreskill.ScopeUserPlugin, coreskill.ScopeUser:
		return true
	}
	return false
}

func scopeIsPlugin(scope coreskill.SkillScope) bool {
	return scope == coreskill.ScopeUserPlugin || scope == coreskill.ScopeProjectPlugin
}

func skillMatchesTab(it skillItem, tab skillTab) bool {
	if tab == skillTabUser {
		return scopeIsUser(it.Scope)
	}
	return !scopeIsUser(it.Scope)
}

func (s *SkillSelector) tabCount(tab skillTab) int {
	count := 0
	for _, it := range s.skills {
		if skillMatchesTab(it, tab) {
			count++
		}
	}
	return count
}

func (s *SkillSelector) firstNonEmptyTab() skillTab {
	if s.tabCount(skillTabProject) > 0 {
		return skillTabProject
	}
	if s.tabCount(skillTabUser) > 0 {
		return skillTabUser
	}
	return skillTabProject
}

func (s *SkillSelector) saveLevelForActiveTab() bool {
	return s.activeTab == skillTabUser
}

func (s *SkillSelector) IsActive() bool {
	return s.active
}

func (s *SkillSelector) Cancel() {
	s.active = false
	s.skills = []skillItem{}
	s.filteredSkills = []skillItem{}
	s.nav.Reset()
	s.nav.Total = 0
}

// applyFilters rebuilds filteredSkills from the active tab + search query.
func (s *SkillSelector) applyFilters() {
	query := strings.ToLower(s.nav.Search)
	s.filteredSkills = s.filteredSkills[:0]
	for _, sk := range s.skills {
		if !skillMatchesTab(sk, s.activeTab) {
			continue
		}
		if query != "" {
			full := strings.ToLower(sk.FullName())
			name := strings.ToLower(sk.Name)
			desc := strings.ToLower(sk.Description)
			if !kit.FuzzyMatch(full, query) &&
				!kit.FuzzyMatch(name, query) &&
				!kit.FuzzyMatch(desc, query) {
				continue
			}
		}
		s.filteredSkills = append(s.filteredSkills, sk)
	}
	s.nav.ResetCursor()
	s.nav.Total = len(s.filteredSkills)
}

func (s *SkillSelector) cycleTab(delta int) {
	tabs := []skillTab{skillTabProject, skillTabUser}
	idx := 0
	for i, t := range tabs {
		if t == s.activeTab {
			idx = i
			break
		}
	}
	n := len(tabs)
	next := tabs[((idx+delta)%n+n)%n]
	s.activeTab = next
	s.applyFilters()
}

func (s *SkillSelector) CycleState() tea.Cmd {
	if len(s.filteredSkills) == 0 || s.nav.Selected >= len(s.filteredSkills) {
		return nil
	}

	selected := &s.filteredSkills[s.nav.Selected]
	newState := selected.State.NextState()
	selected.State = newState

	fullName := selected.FullName()

	for i := range s.skills {
		if s.skills[i].FullName() == fullName {
			s.skills[i].State = newState
			break
		}
	}

	if s.registry != nil {
		_ = s.registry.SetState(fullName, newState, s.saveLevelForActiveTab())
	}

	return func() tea.Msg {
		return SkillCycleMsg{
			SkillName: fullName,
			NewState:  newState,
		}
	}
}

func (s *SkillSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyTab, tea.KeyRight:
		s.cycleTab(+1)
		return nil
	case tea.KeyShiftTab, tea.KeyLeft:
		s.cycleTab(-1)
		return nil
	case tea.KeyEnter:
		return s.CycleState()
	}

	searchChanged, consumed := s.nav.HandleKey(key)
	if searchChanged {
		s.applyFilters()
	}
	if consumed {
		return nil
	}

	if key.Type == tea.KeyEsc {
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}

	return nil
}

// ── Rendering ──────────────────────────────────────────────────────────────────

func (s *SkillSelector) Render() string {
	if !s.active {
		return ""
	}

	panel := kit.Panel{Width: s.width, Height: s.height}

	// Each item renders on 2 lines (row + spacer); the selected item adds
	// 1 hint sub-line. Reserve 2 lines for more-above/more-below indicators.
	s.nav.MaxVisible = max(3, (panel.BodyHeight()-2)/2)
	s.nav.EnsureVisible()

	var sb strings.Builder

	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderTabs())
	sb.WriteString("\n\n")
	sb.WriteString(kit.RenderSearchBox(kit.SearchBoxOpts{
		Query:       s.nav.Search,
		Placeholder: "Type to filter skills...",
		Filtered:    len(s.filteredSkills),
		Total:       s.tabCount(s.activeTab),
		Width:       panel.ContentWidth(),
	}))
	sb.WriteString("\n\n")

	var body strings.Builder
	if len(s.filteredSkills) == 0 {
		body.WriteString(s.renderEmpty())
	} else {
		s.renderItemList(&body, panel)
	}
	sb.WriteString(panel.PadViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(s.renderHints())

	return panel.Wrap(sb.String())
}

func (s *SkillSelector) renderTabs() string {
	tabs := []kit.PanelTab{
		{Name: skillTabProject.String(), Count: s.tabCount(skillTabProject), Show: true},
		{Name: skillTabUser.String(), Count: s.tabCount(skillTabUser), Show: true},
	}
	return kit.RenderPanelTabs(tabs, int(s.activeTab))
}

func (s *SkillSelector) renderEmpty() string {
	if len(s.skills) == 0 {
		return kit.DimStyle().PaddingLeft(2).Render("No skills available")
	}
	if s.tabCount(s.activeTab) == 0 {
		return kit.DimStyle().PaddingLeft(2).Render(
			fmt.Sprintf("No %s skills — press Tab to switch tabs",
				strings.ToLower(s.activeTab.String())))
	}
	return kit.DimStyle().PaddingLeft(2).Render("No skills match the filter")
}

func (s *SkillSelector) renderItemList(sb *strings.Builder, panel kit.Panel) {
	startIdx, endIdx := s.nav.VisibleRange()

	if startIdx > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	maxNameLen := 12
	for i := startIdx; i < endIdx; i++ {
		if l := len(s.filteredSkills[i].FullName()); l > maxNameLen {
			maxNameLen = l
		}
	}
	maxNameLen = min(maxNameLen, 32)

	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	badge := kit.BadgeStyle()

	for i := startIdx; i < endIdx; i++ {
		sk := s.filteredSkills[i]

		var statusIcon string
		var statusStyle lipgloss.Style
		switch sk.State {
		case coreskill.StateActive:
			statusIcon = "●"
			statusStyle = kit.SelectorStatusConnected()
		case coreskill.StateEnable:
			statusIcon = "◐"
			statusStyle = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Warning)
		default:
			statusIcon = "○"
			statusStyle = kit.SelectorStatusNone()
		}

		name := kit.TruncateText(sk.FullName(), maxNameLen)
		paddedName := name + strings.Repeat(" ", max(0, maxNameLen-len(name)))

		badgeText := ""
		if scopeIsPlugin(sk.Scope) && sk.Namespace != "" {
			badgeText = "[Plugin: " + sk.Namespace + "]"
		}

		// Width budget for one row, accounting for the panel's Padding(1, 2)
		// (4 cols total) plus the row's own decoration:
		//   2 ("> ") + 1 (icon) + 1 (space) + name + 2 (sep) + desc
		//   [+ 1 space + badge]
		// The trailing -4 is a right-margin safety buffer.
		rowFixed := 2 + 1 + 1 + maxNameLen + 2
		if badgeText != "" {
			rowFixed += 1 + len(badgeText)
		}
		descWidth := max(15, panel.ContentWidth()-4-rowFixed-4)
		desc := kit.TruncateText(sk.Description, descWidth)

		line := fmt.Sprintf("%s %s  %s",
			statusStyle.Render(statusIcon),
			paddedName,
			descStyle.Render(desc),
		)
		if badgeText != "" {
			line += " " + badge.Render(badgeText)
		}

		// Render the row without SelectorSelectedStyle/SelectorItemStyle's
		// PaddingLeft(2) so the row's left edge lines up with tabs/search/
		// separator. Width(...) right-pads each row to the full inner content
		// area so the right edge also matches the separator line.
		rowWidth := max(20, panel.ContentWidth()-4)
		if i == s.nav.Selected {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.TextBright).
				Bold(true).
				Width(rowWidth).
				Render("> " + line))
		} else {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Text).
				Width(rowWidth).
				Render("  " + line))
		}
		sb.WriteString("\n")

		// Argument hint sub-line aligned under the skill name (4 cols in:
		// 2 cursor + 1 icon + 1 space).
		if i == s.nav.Selected && sk.Hint != "" {
			subStyle := lipgloss.NewStyle().
				Foreground(kit.CurrentTheme.Muted).
				PaddingLeft(4)
			hintLineWidth := max(10, panel.ContentWidth()-8)
			sb.WriteString(subStyle.Render(kit.TruncateText("hint: "+sk.Hint, hintLineWidth)))
			sb.WriteString("\n")
		}

		// Spacer for breathing room between rows.
		if i < endIdx-1 {
			sb.WriteString("\n")
		}
	}

	if endIdx < len(s.filteredSkills) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}

func (s *SkillSelector) renderHints() string {
	return kit.HintLine(
		"↑/↓ navigate",
		"Enter cycle state",
		"←/→/Tab switch tab",
		"Esc close",
	)
}
