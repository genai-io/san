package input

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/setting"
)

type personaItem struct {
	Name        string
	Description string
	Scope       string // "built-in" / "user" / "project"
	IsCurrent   bool
}

// PersonaSelectedMsg is emitted when the user picks a persona in the selector;
// the app applies it (persist + hot-patch) via OverlayDeps.SetActivePersona.
type PersonaSelectedMsg struct {
	Name string
}

// PersonaSelector is the interactive /persona picker — a single-select list of
// the available personas that switches the active one on Enter.
type PersonaSelector struct {
	active      bool
	items       []personaItem
	selectedIdx int
	width       int
	height      int
	registry    *persona.Registry
	settingSvc  *setting.Settings
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
		})
	}

	s.active = true
	s.selectedIdx = 0
	s.width = width
	s.height = height
	for i, it := range s.items {
		if it.IsCurrent {
			s.selectedIdx = i
			break
		}
	}
	return nil
}

func (s *PersonaSelector) IsActive() bool { return s.active }

func (s *PersonaSelector) Cancel() {
	s.active = false
	s.items = nil
	s.selectedIdx = 0
}

func (s *PersonaSelector) Select() tea.Cmd {
	if s.selectedIdx >= len(s.items) {
		return nil
	}
	name := s.items[s.selectedIdx].Name
	return func() tea.Msg { return PersonaSelectedMsg{Name: name} }
}

func (s *PersonaSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.Type {
	case tea.KeyUp, tea.KeyCtrlP:
		if s.selectedIdx > 0 {
			s.selectedIdx--
		}
		return nil
	case tea.KeyDown, tea.KeyCtrlN:
		if s.selectedIdx < len(s.items)-1 {
			s.selectedIdx++
		}
		return nil
	case tea.KeyEnter:
		return s.Select()
	case tea.KeyEsc:
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}

	switch key.String() {
	case "j":
		if s.selectedIdx < len(s.items)-1 {
			s.selectedIdx++
		}
	case "k":
		if s.selectedIdx > 0 {
			s.selectedIdx--
		}
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

	var body strings.Builder
	for i, item := range s.items {
		isSelected := i == s.selectedIdx

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
	sb.WriteString(dimStyle.Render("↑/↓ navigate · Enter switch · Esc cancel"))

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
	if len(view) > visible {
		// Keep the selected row in view.
		start := 0
		if s.selectedIdx >= visible {
			start = s.selectedIdx - visible + 1
		}
		end := start + visible
		if end > len(view) {
			end = len(view)
		}
		view = view[start:end]
	}
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

// UpdatePersona applies a picked persona via the app callback and shows status.
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
	}
	return nil, false
}
