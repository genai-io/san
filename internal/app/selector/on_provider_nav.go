// Provider selector: cursor/tab navigation, model search, and key routing.
package selector

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

func (s *Provider) ensureVisible() {
	if s.selectedIdx < s.scrollOffset {
		s.scrollOffset = s.selectedIdx
	}
	if s.selectedIdx >= s.scrollOffset+s.maxVisible {
		s.scrollOffset = s.selectedIdx - s.maxVisible + 1
	}
}

func (s *Provider) MoveUp() {
	for s.selectedIdx > 0 {
		s.selectedIdx--
		if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
			break
		}
	}
	if s.selectedIdx == 0 {
		s.searchFocused = true
	}
	s.ensureVisible()
}

func (s *Provider) MoveDown() {
	for s.selectedIdx < len(s.visibleItems)-1 {
		s.selectedIdx++
		if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
			break
		}
	}
	s.searchFocused = false
	s.ensureVisible()
}

func (s *Provider) switchTab(t providerTab) {
	if t == s.activeTab {
		return
	}
	s.activeTab = t
	s.resetNavigation()
	s.resetModelSearch()
	s.resetConnectionResult()
	s.expandedProviderIdx = -1
	s.apiKeyActive = false
	s.rebuildVisibleItems()
}

func (s *Provider) NextTab() { s.switchTab((s.activeTab + 1) % 2) }
func (s *Provider) PrevTab() { s.switchTab((s.activeTab + 1 + 2) % 2) }

func (s *Provider) GoBack() bool {
	if s.apiKeyActive {
		s.apiKeyActive = false
		return true
	}
	if s.expandedProviderIdx >= 0 {
		s.expandedProviderIdx = -1
		s.resetConnectionResult()
		s.rebuildVisibleItems()
		return true
	}
	return false
}

func (s *Provider) clearModelSearch() bool {
	if s.searchQuery == "" {
		return false
	}
	s.searchQuery = ""
	s.searchFocused = false
	s.rebuildVisibleItems()
	return true
}

func (s *Provider) trimModelSearch() {
	if len(s.searchQuery) == 0 {
		return
	}
	s.searchQuery = s.searchQuery[:len(s.searchQuery)-1]
	if s.searchQuery == "" {
		// Empty query means we're no longer typing in the search box, so Space
		// returns to marking models rather than inserting a literal space.
		s.searchFocused = false
	}
	s.rebuildVisibleItems()
}

func (s *Provider) appendModelSearch(text string) {
	s.searchQuery += text
	s.searchFocused = true
	s.rebuildVisibleItems()
}

func (s *Provider) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	// Route to API key input if active
	if s.apiKeyActive {
		return s.handleAPIKeyInput(key)
	}

	// Route to confirm-remove if active
	if s.confirmRemoveActive {
		return s.handleConfirmRemove(key)
	}

	switch key.String() {
	case "tab":
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case "shift+tab":
		if s.searchQuery == "" {
			s.PrevTab()
		}
		return nil

	case "up", "ctrl+p":
		s.MoveUp()
		return nil

	case "down", "ctrl+n":
		s.MoveDown()
		return nil

	case "enter":
		return s.Select()

	case "right":
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case "left":
		if s.searchQuery == "" && !s.GoBack() {
			s.PrevTab()
		}
		return nil

	case "esc":
		if s.clearModelSearch() {
			return nil
		}
		if s.GoBack() {
			return nil
		}
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }

	case "backspace":
		s.trimModelSearch()
		return nil

	case "space":
		if s.activeTab == providerTabModels && !s.searchFocused {
			return s.toggleModel()
		}
		s.appendModelSearch(" ")
		return nil

	case "ctrl+e":
		return s.handleCredentialEdit()

	case "ctrl+d":
		return s.handleCredentialRemove()

	default:
		// Typed text capture. Vim navigation takes priority while the model
		// search is empty (mirrors every other selector); otherwise the
		// printable rune is search input. l/h switch tabs since this is tabbed.
		if text := key.Key().Text; text != "" {
			if s.searchQuery == "" {
				switch key.String() {
				case "j":
					s.MoveDown()
					return nil
				case "k":
					s.MoveUp()
					return nil
				case "l":
					s.NextTab()
					return nil
				case "h":
					if !s.GoBack() {
						s.PrevTab()
					}
					return nil
				}
			}
			s.appendModelSearch(text)
			return nil
		}
	}

	return nil
}
