package input

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
)

type personaItem struct {
	Name        string
	Description string
	Scope       string // "built-in" / "user" / "project"
	IsCurrent   bool
	Builtin     bool
}

// PersonaSelectedMsg is emitted when the user picks a persona to switch to; the
// app applies it (persist + hot-patch) via OverlayDeps.SetActivePersona.
type PersonaSelectedMsg struct {
	Name string
}

// PersonaOpenMsg asks the app to open the named persona's files for editing,
// using the OS default handler (no $EDITOR / terminal editor required).
type PersonaOpenMsg struct {
	Name string
}

// PersonaDeleteMsg asks the app to delete the named persona's directory.
type PersonaDeleteMsg struct {
	Name string
}

// PersonaSelector is the interactive /persona picker — a single-select list of
// the available personas. Enter switches; Ctrl+O opens for editing; Ctrl+D
// deletes (with a confirm); Esc cancels.
type PersonaSelector struct {
	active        bool
	confirmDelete bool
	items         []personaItem
	nav           kit.ListNav
	width         int
	height        int
	registry      *persona.Registry
	settingSvc    *setting.Settings
}

func NewPersonaSelector(reg *persona.Registry, settingSvc *setting.Settings) PersonaSelector {
	return PersonaSelector{registry: reg, settingSvc: settingSvc}
}

func personaScopeLabel(p *persona.Persona) string {
	switch p.Scope {
	case persona.ScopeProject:
		return "project"
	case persona.ScopeUser:
		return "user"
	default:
		return "built-in"
	}
}

// EnterSelect opens the picker: it lists the registry's personas and marks the
// active one (settings.persona; empty = the built-in default).
func (s *PersonaSelector) EnterSelect(width, height int) error {
	if s.registry == nil {
		return fmt.Errorf("persona registry unavailable")
	}

	current := persona.DefaultName
	if s.settingSvc != nil {
		if snap := s.settingSvc.Snapshot(); snap != nil && snap.Persona != "" {
			current = snap.Persona
		}
	}

	list := s.registry.List()
	s.items = make([]personaItem, 0, len(list))
	for _, p := range list {
		s.items = append(s.items, personaItem{
			Name:        p.Name,
			Description: p.Description,
			Scope:       personaScopeLabel(p),
			IsCurrent:   p.Name == current,
			Builtin:     p.IsBuiltin(),
		})
	}

	s.active = true
	s.confirmDelete = false
	s.width = width
	s.height = height
	s.nav.Reset()
	s.nav.Total = len(s.items)
	s.nav.MaxVisible = s.bodyHeight()
	for i, it := range s.items {
		if it.IsCurrent {
			s.nav.Selected = i
			break
		}
	}
	s.nav.EnsureVisible()
	return nil
}

func (s *PersonaSelector) IsActive() bool { return s.active }

func (s *PersonaSelector) Cancel() {
	s.active = false
	s.confirmDelete = false
	s.items = nil
	s.nav.Reset()
}

func (s *PersonaSelector) Select() tea.Cmd {
	if s.nav.Selected >= len(s.items) {
		return nil
	}
	name := s.items[s.nav.Selected].Name
	return func() tea.Msg { return PersonaSelectedMsg{Name: name} }
}

func (s *PersonaSelector) selected() (personaItem, bool) {
	if s.nav.Selected < 0 || s.nav.Selected >= len(s.items) {
		return personaItem{}, false
	}
	return s.items[s.nav.Selected], true
}

func (s *PersonaSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	// Delete confirmation: only "y" confirms; anything else backs out.
	if s.confirmDelete {
		if key.String() == "y" || key.String() == "Y" {
			it, ok := s.selected()
			s.Cancel()
			if ok {
				return func() tea.Msg { return PersonaDeleteMsg{Name: it.Name} }
			}
			return nil
		}
		s.confirmDelete = false
		return nil
	}

	switch key.String() {
	case "up", "ctrl+p", "k":
		s.nav.MoveUp()
	case "down", "ctrl+n", "j":
		s.nav.MoveDown()
	case "enter":
		return s.Select()
	case "ctrl+o":
		if it, ok := s.selected(); ok && !it.Builtin {
			s.Cancel()
			return func() tea.Msg { return PersonaOpenMsg{Name: it.Name} }
		}
	case "ctrl+d":
		if it, ok := s.selected(); ok && !it.Builtin {
			s.confirmDelete = true
		}
	case "esc":
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}
	return nil
}

func (s *PersonaSelector) Render() string {
	if !s.active {
		return ""
	}

	var sb strings.Builder
	dimStyle := kit.DimStyle()

	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	sb.WriteString(kit.SelectorTitleStyle().Render("Persona"))
	sb.WriteString("\n\n")

	const nameCol = 22
	metaMax := max(16, s.contentWidth()-nameCol-12)

	startIdx, endIdx := s.nav.VisibleRange()

	var body strings.Builder
	for i := startIdx; i < endIdx; i++ {
		item := s.items[i]
		isSelected := i == s.nav.Selected

		marker := "[ ]"
		markerStyle := kit.SelectorStatusNone()
		if item.IsCurrent {
			marker = "[*]"
			markerStyle = kit.SelectorStatusConnected()
		}

		meta := item.Scope
		if item.Description != "" {
			meta = item.Scope + " · " + item.Description
		}
		meta = personaTruncate(meta, metaMax)

		line := kit.FormatAlignedRow(markerStyle.Render(marker), item.Name, nameCol, dimStyle.Render(meta))
		body.WriteString(kit.RenderSelectableRow(line, isSelected))
		body.WriteString("\n")
	}
	sb.WriteString(s.renderViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(s.sepLine())
	sb.WriteString("\n")
	if it, ok := s.selected(); s.confirmDelete && ok {
		warn := lipgloss.NewStyle().Foreground(kit.AdaptiveColor{Dark: "#F87171", Light: "#DC2626"})
		sb.WriteString(warn.Render("Delete persona '" + it.Name + "' from disk?  y = yes · any other key = no"))
	} else {
		sb.WriteString(dimStyle.Render("↑/↓ navigate · Enter switch · Ctrl+O open · Ctrl+D delete · Esc cancel"))
	}

	content := sb.String()
	box := lipgloss.NewStyle().
		Width(s.contentWidth()).
		Height(s.boxHeight()).
		Padding(1, 2).
		Render(content)

	return lipgloss.Place(s.width, s.height-2, lipgloss.Center, lipgloss.Top, box)
}

// personaTruncate trims s to at most maxW display columns, adding an ellipsis.
func personaTruncate(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > maxW {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func (s *PersonaSelector) contentWidth() int { return max(60, s.width-6) }
func (s *PersonaSelector) boxHeight() int    { return max(18, s.height-4) }
func (s *PersonaSelector) bodyHeight() int   { return max(6, s.boxHeight()-10) }

func (s *PersonaSelector) renderViewport(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	visible := s.bodyHeight()
	if visible <= 0 {
		return ""
	}
	view := lines
	for len(view) < visible {
		view = append(view, "")
	}
	return strings.Join(view, "\n") + "\n"
}

func (s *PersonaSelector) sepLine() string {
	sepStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDim)
	return sepStyle.Render(strings.Repeat("─", s.contentWidth()-8))
}

// --- Persona Runtime ---

// UpdatePersona applies persona picker actions (switch / open / delete) via the
// app callbacks on OverlayDeps and shows a status line.
func UpdatePersona(deps OverlayDeps, state *PersonaSelector, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case PersonaSelectedMsg:
		state.Cancel()
		if deps.SetActivePersona != nil {
			if err := deps.SetActivePersona(msg.Name); err != nil {
				token := deps.State.Provider.SetStatusMessage("Persona switch failed: " + err.Error())
				return kit.StatusTimer(4*time.Second, token), true
			}
		}
		status := "Persona: " + msg.Name
		if msg.Name == "" || msg.Name == persona.DefaultName {
			status = "Persona: default (built-in San)"
		}
		token := deps.State.Provider.SetStatusMessage(status)
		return kit.StatusTimer(3*time.Second, token), true

	case PersonaOpenMsg:
		if deps.OpenPersona != nil {
			return deps.OpenPersona(msg.Name), true
		}
		return nil, true

	case PersonaDeleteMsg:
		if deps.DeletePersona != nil {
			if err := deps.DeletePersona(msg.Name); err != nil {
				token := deps.State.Provider.SetStatusMessage("Delete failed: " + err.Error())
				return kit.StatusTimer(4*time.Second, token), true
			}
		}
		token := deps.State.Provider.SetStatusMessage("Deleted persona: " + msg.Name)
		return kit.StatusTimer(3*time.Second, token), true
	}
	return nil, false
}
