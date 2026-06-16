package input

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	coreskill "github.com/genai-io/san/internal/skill"
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

// skillTab identifies a category tab in the skill selector. The values double
// as indices into the selector's tabbedList tab order.
type skillTab int

const (
	skillTabProject skillTab = iota
	skillTabUser
)

// SkillSelector holds the state for the skill selector overlay. The tab/filter/
// keypress/frame mechanics live in the embedded tabbedList; this type owns
// skill loading, the row layout, and the state-cycling action.
type SkillSelector struct {
	registry *coreskill.Registry
	list     tabbedList[skillItem]
}

type SkillState struct {
	Selector            SkillSelector
	PendingInstructions string
	PendingArgs         string
	PendingFullName     string
	// PendingPluginRoot is the plugin directory the pending invocation
	// originated from (empty for non-plugin skills). When the invocation
	// is consumed, the runner passes it to SubmitToAgent so the resulting
	// agent turn sees PLUGIN_ROOT pointing at this plugin.
	PendingPluginRoot string
}

// ConsumeInvocation extracts the pending skill invocation and clears pending
// state. Returns (displayMsg, fullMsg, pluginRoot):
//   - displayMsg is shown in chat UI
//   - fullMsg embeds the skill instructions, wrapped with a <command-name> tag
//     so the Skill tool can detect and skip a redundant call
//   - pluginRoot is the plugin directory the invocation came from (empty
//     for non-plugin skills); the caller forwards it to SubmitToAgent so
//     hooks/tools spawned during this turn see PLUGIN_ROOT pointing at it
func (s *SkillState) ConsumeInvocation() (displayMsg, fullMsg, pluginRoot string) {
	displayMsg = s.PendingArgs
	if displayMsg == "" {
		displayMsg = "Execute the skill."
	}
	fullMsg = displayMsg
	if s.PendingInstructions != "" && s.PendingFullName != "" {
		fullMsg = "<command-name>" + s.PendingFullName + "</command-name>\n\n" +
			s.PendingInstructions + "\n\n" + displayMsg
	}
	pluginRoot = s.PendingPluginRoot
	s.ClearPending()
	return displayMsg, fullMsg, pluginRoot
}

// SetPending stages a slash-command invocation. Caller may set PendingArgs
// separately; this helper covers the always-paired name+instructions fields.
func (s *SkillState) SetPending(fullName, instructions string) {
	s.PendingFullName = fullName
	s.PendingInstructions = instructions
}

// ClearPending resets pending skill state without activating.
func (s *SkillState) ClearPending() {
	s.PendingInstructions = ""
	s.PendingArgs = ""
	s.PendingFullName = ""
	s.PendingPluginRoot = ""
}

func NewSkillSelector(reg *coreskill.Registry) SkillSelector {
	return SkillSelector{
		registry: reg,
		list: tabbedList[skillItem]{
			tabs: []tabSpec{
				{name: "Project"},
				{name: "User"},
			},
			noun:        "skills",
			placeholder: "Type to filter skills...",
			hints:       []string{"↑/↓ navigate", "Enter cycle state", "←/→/Tab switch tab", "Esc close"},
			matchesTab:  skillMatchesTab,
			searchKeys:  func(sk skillItem) []string { return []string{sk.FullName(), sk.Name, sk.Description} },
			nav:         kit.ListNav{MaxVisible: 10},
		},
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

	skills := make([]skillItem, 0, len(allSkills))
	for _, sk := range allSkills {
		userLevel := scopeIsUser(sk.Scope)
		state := sk.State
		if levelState, ok := statesByLevel[userLevel][sk.FullName()]; ok {
			state = levelState
		}
		skills = append(skills, skillItem{
			Name:        sk.Name,
			Namespace:   sk.Namespace,
			Description: sk.Description,
			Hint:        sk.ArgumentHint,
			State:       state,
			Scope:       sk.Scope,
		})
	}

	s.list.load(skills, width, height)
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

func skillMatchesTab(it skillItem, tab int) bool {
	if skillTab(tab) == skillTabUser {
		return scopeIsUser(it.Scope)
	}
	return !scopeIsUser(it.Scope)
}

func (s *SkillSelector) saveLevelForActiveTab() bool {
	return s.list.activeTab == int(skillTabUser)
}

func (s *SkillSelector) IsActive() bool { return s.list.active }

func (s *SkillSelector) CycleState() tea.Cmd {
	if len(s.list.filtered) == 0 || s.list.nav.Selected >= len(s.list.filtered) {
		return nil
	}

	selected := &s.list.filtered[s.list.nav.Selected]
	newState := selected.State.NextState()
	selected.State = newState

	fullName := selected.FullName()

	for i := range s.list.items {
		if s.list.items[i].FullName() == fullName {
			s.list.items[i].State = newState
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
	return s.list.handleKey(key, s.CycleState)
}

// ── Rendering ──────────────────────────────────────────────────────────────────

func (s *SkillSelector) Render() string {
	return s.list.render(s.renderItemList)
}

func (s *SkillSelector) renderItemList(sb *strings.Builder, panel kit.Panel) {
	startIdx, endIdx := s.list.nav.VisibleRange()

	if startIdx > 0 {
		sb.WriteString(kit.MoreAbove())
		sb.WriteString("\n")
	}

	maxNameLen := 12
	for i := startIdx; i < endIdx; i++ {
		if l := len(s.list.filtered[i].FullName()); l > maxNameLen {
			maxNameLen = l
		}
	}
	maxNameLen = min(maxNameLen, 32)

	descStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	badge := kit.BadgeStyle()

	for i := startIdx; i < endIdx; i++ {
		sk := s.list.filtered[i]

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

		// Render the row without the selector row styles' PaddingLeft(2) so
		// the row's left edge lines up with tabs/search/
		// separator. Width(...) right-pads each row to the full inner content
		// area so the right edge also matches the separator line.
		rowWidth := max(20, panel.ContentWidth()-4)
		sb.WriteString(kit.RenderPanelRow(line, i == s.list.nav.Selected, rowWidth))
		sb.WriteString("\n")

		// Argument hint sub-line aligned under the skill name (4 cols in:
		// 2 cursor + 1 icon + 1 space).
		if i == s.list.nav.Selected && sk.Hint != "" {
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

	if endIdx < len(s.list.filtered) {
		sb.WriteString(kit.MoreBelow())
		sb.WriteString("\n")
	}
}
