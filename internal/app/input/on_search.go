package input

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/search"
	"github.com/genai-io/san/internal/secret"
	"github.com/genai-io/san/internal/setting"
)

type searchItem struct {
	Name        search.ProviderName
	DisplayName string
	EnvVars     []string
	Available   bool
	IsCurrent   bool
}

type searchSelectedMsg struct {
	Provider search.ProviderName
}

type SearchSelector struct {
	active     bool
	items      []searchItem
	nav        kit.ListNav
	width      int
	height     int
	store      *llm.Store
	settingSvc *setting.Settings

	apiKeyActive bool
	apiKeyEnvVar string
	apiKeyInput  textinput.Model
}

func NewSearchSelector(settingSvc *setting.Settings) SearchSelector {
	return SearchSelector{settingSvc: settingSvc}
}

func (s *SearchSelector) Enter(store *llm.Store, width, height int) error {
	if store == nil {
		var err error
		store, err = llm.NewStore()
		if err != nil {
			return fmt.Errorf("failed to open provider store: %w", err)
		}
	}

	currentName := store.GetSearchProvider()
	if currentName == "" {
		currentName = string(search.ProviderExa)
	}

	allMeta := search.AllProviders()
	s.items = make([]searchItem, 0, len(allMeta))
	for _, meta := range allMeta {
		available := !meta.RequiresAPIKey
		if !available {
			for _, envVar := range meta.EnvVars {
				if secret.Resolve(envVar) != "" {
					available = true
					break
				}
			}
		}
		s.items = append(s.items, searchItem{
			Name:        meta.Name,
			DisplayName: meta.DisplayName,
			EnvVars:     meta.EnvVars,
			Available:   available,
			IsCurrent:   string(meta.Name) == currentName,
		})
	}

	s.active = true
	s.width = width
	s.height = height
	s.store = store

	s.nav.Reset()
	s.nav.Total = len(s.items)
	for i, item := range s.items {
		if item.IsCurrent {
			s.nav.Selected = i
			break
		}
	}

	return nil
}

func (s *SearchSelector) IsActive() bool {
	return s.active
}

func (s *SearchSelector) Cancel() {
	s.active = false
	s.items = nil
	s.nav.Reset()
	s.store = nil
}

func (s *SearchSelector) Select() tea.Cmd {
	if s.nav.Selected >= len(s.items) {
		return nil
	}

	selected := s.items[s.nav.Selected]
	if !selected.Available {
		s.openAPIKeyInput()
		return nil
	}

	if s.settingSvc != nil {
		s.settingSvc.SetSearchProvider(string(selected.Name))
	}
	if s.store != nil {
		_ = s.store.SetSearchProvider(string(selected.Name))
	}

	for i := range s.items {
		s.items[i].IsCurrent = s.items[i].Name == selected.Name
	}

	return func() tea.Msg {
		return searchSelectedMsg{Provider: selected.Name}
	}
}

func (s *SearchSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if s.apiKeyActive {
		return s.handleAPIKeyInput(key)
	}

	switch key.String() {
	case "up", "ctrl+p", "k":
		s.nav.MoveUp()
	case "down", "ctrl+n", "j":
		s.nav.MoveDown()
	case "enter":
		return s.Select()
	case "esc":
		s.Cancel()
		return func() tea.Msg {
			return kit.DismissedMsg{}
		}
	case "e":
		s.openAPIKeyInput()
	}

	return nil
}

func (s *SearchSelector) selectedHasEnvVars() bool {
	return s.nav.Selected < len(s.items) && len(s.items[s.nav.Selected].EnvVars) > 0
}

func (s *SearchSelector) openAPIKeyInput() {
	if !s.selectedHasEnvVars() {
		return
	}
	s.apiKeyActive = true
	s.apiKeyEnvVar = s.items[s.nav.Selected].EnvVars[0]
	ti := textinput.New()
	ti.Placeholder = s.apiKeyEnvVar
	ti.EchoMode = textinput.EchoPassword
	ti.Focus()
	s.apiKeyInput = ti
}

func (s *SearchSelector) handleAPIKeyInput(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter":
		value := strings.TrimSpace(s.apiKeyInput.Value())
		if value == "" {
			return nil
		}
		if store := secret.Default(); store != nil {
			_ = store.Set(s.apiKeyEnvVar, value)
		}
		os.Setenv(s.apiKeyEnvVar, value)

		for i := range s.items {
			for _, ev := range s.items[i].EnvVars {
				if ev == s.apiKeyEnvVar {
					s.items[i].Available = true
				}
			}
		}
		s.apiKeyActive = false
		return s.Select()
	case "esc":
		s.apiKeyActive = false
		return nil
	default:
		s.apiKeyInput, _ = s.apiKeyInput.Update(key)
		return nil
	}
}

func (s *SearchSelector) Render() string {
	if !s.active {
		return ""
	}

	panel := s.panel()
	var sb strings.Builder
	dimStyle := kit.DimStyle()

	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")

	sb.WriteString(kit.SelectorTitleStyle().Render("Search Engine"))
	sb.WriteString("\n\n")

	var body strings.Builder
	const nameCol = 20
	for i, item := range s.items {
		isSelected := i == s.nav.Selected

		marker := "[ ]"
		markerStyle := kit.SelectorStatusNone()
		if item.IsCurrent {
			marker = "[*]"
			markerStyle = kit.SelectorStatusConnected()
		}

		envInfo := ""
		if len(item.EnvVars) > 0 {
			envInfo = kit.RenderEnvVarStatus(item.EnvVars[0])
		} else {
			envInfo = dimStyle.Render("no key required")
		}

		line := kit.FormatAlignedRow(markerStyle.Render(marker), item.DisplayName, nameCol, envInfo)
		body.WriteString(kit.RenderSelectableRow(line, isSelected))
		body.WriteString("\n")

		if s.apiKeyActive && isSelected {
			label := dimStyle.Render(s.apiKeyEnvVar + ": ")
			inputBg := kit.AdaptiveColor{Dark: "#1E293B", Light: "#F1F5F9"}
			boxStyle := lipgloss.NewStyle().Background(inputBg).Padding(0, 1)
			body.WriteString("      " + boxStyle.Render(label+s.apiKeyInput.View()))
			body.WriteString("\n")
		}
	}
	sb.WriteString(panel.PadViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	if s.apiKeyActive {
		sb.WriteString(dimStyle.Render("Paste API key · Enter confirm · Esc cancel"))
	} else {
		hint := "↑/↓ navigate · Enter select · Esc cancel"
		if s.selectedHasEnvVars() {
			hint = "↑/↓ navigate · Enter select · e edit key · Esc cancel"
		}
		sb.WriteString(dimStyle.Render(hint))
	}

	return panel.Wrap(sb.String())
}

// panel supplies the shared selector sizing/frame primitives.
func (s *SearchSelector) panel() kit.Panel { return kit.Panel{Width: s.width, Height: s.height} }

// --- Search Runtime ---

func UpdateSearch(deps OverlayDeps, state *SearchSelector, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case searchSelectedMsg:
		state.Cancel()
		token := deps.State.Provider.SetStatusMessage(fmt.Sprintf("Search engine: %s", msg.Provider))
		return kit.StatusTimer(3*time.Second, token), true
	}
	return nil, false
}
