// Provider selector: cursor/tab navigation, model search, and key routing.
package input

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/kit"
)

func (s *ProviderSelector) ensureVisible() {
	if s.selectedIdx < s.scrollOffset {
		s.scrollOffset = s.selectedIdx
	}
	if s.selectedIdx >= s.scrollOffset+s.maxVisible {
		s.scrollOffset = s.selectedIdx - s.maxVisible + 1
	}
}

func (s *ProviderSelector) MoveUp() {
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

func (s *ProviderSelector) MoveDown() {
	for s.selectedIdx < len(s.visibleItems)-1 {
		s.selectedIdx++
		if s.visibleItems[s.selectedIdx].Kind != providerItemProviderHeader {
			break
		}
	}
	s.searchFocused = false
	s.ensureVisible()
}

func (s *ProviderSelector) switchTab(t providerTab) {
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

func (s *ProviderSelector) NextTab() { s.switchTab((s.activeTab + 1) % 2) }
func (s *ProviderSelector) PrevTab() { s.switchTab((s.activeTab + 1 + 2) % 2) }

func (s *ProviderSelector) GoBack() bool {
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

func (s *ProviderSelector) clearModelSearch() bool {
	if s.searchQuery == "" {
		return false
	}
	s.searchQuery = ""
	s.searchFocused = false
	s.rebuildVisibleItems()
	return true
}

func (s *ProviderSelector) trimModelSearch() {
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

func (s *ProviderSelector) appendModelSearch(text string) {
	s.searchQuery += text
	s.searchFocused = true
	s.rebuildVisibleItems()
}

func (s *ProviderSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	// Route to API key input if active
	if s.apiKeyActive {
		return s.handleAPIKeyInput(key)
	}

	// Route to confirm-remove if active
	if s.confirmRemoveActive {
		return s.handleConfirmRemove(key)
	}

	switch key.Type {
	case tea.KeyTab:
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case tea.KeyShiftTab:
		if s.searchQuery == "" {
			s.PrevTab()
		}
		return nil

	case tea.KeyUp, tea.KeyCtrlP:
		s.MoveUp()
		return nil

	case tea.KeyDown, tea.KeyCtrlN:
		s.MoveDown()
		return nil

	case tea.KeyEnter:
		return s.Select()

	case tea.KeyRight:
		if s.searchQuery == "" {
			s.NextTab()
		}
		return nil

	case tea.KeyLeft:
		if s.searchQuery == "" && !s.GoBack() {
			s.PrevTab()
		}
		return nil

	case tea.KeyEsc:
		if s.clearModelSearch() {
			return nil
		}
		if s.GoBack() {
			return nil
		}
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }

	case tea.KeyBackspace:
		s.trimModelSearch()
		return nil

	case tea.KeySpace:
		if s.activeTab == providerTabModels && !s.searchFocused {
			return s.toggleModel()
		}
		s.appendModelSearch(" ")
		return nil

	case tea.KeyRunes:
		s.appendModelSearch(string(key.Runes))
		return nil

	case tea.KeyCtrlE:
		return s.handleCredentialEdit()

	case tea.KeyCtrlD:
		return s.handleCredentialRemove()
	}

	// Vim navigation (only when search query is empty)
	if s.searchQuery == "" {
		switch key.String() {
		case "j":
			s.MoveDown()
		case "k":
			s.MoveUp()
		case "l":
			s.NextTab()
		case "h":
			if !s.GoBack() {
				s.PrevTab()
			}
		}
	}

	return nil
}
