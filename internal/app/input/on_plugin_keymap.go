package input

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// HandleKeypress handles a keypress and returns a command if needed.
func (s *PluginSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if s.level == pluginLevelAddMarketplace {
		return s.handleAddMarketplaceKeypress(key)
	}
	if s.level == pluginLevelDetail || s.level == pluginLevelInstallOptions {
		return s.handleDetailKeypress(key)
	}
	if s.level == pluginLevelBrowsePlugins {
		return s.handleBrowseKeypress(key)
	}
	return s.handleListKeypress(key)
}

// HandlePaste appends bracketed-paste content to the marketplace-source field.
// A source is a single token (URL, owner/repo, or local path), so embedded
// newlines and surrounding whitespace are stripped rather than inserted.
func (s *PluginSelector) HandlePaste(content string) tea.Cmd {
	if s.level != pluginLevelAddMarketplace {
		return nil
	}
	content = strings.NewReplacer("\r", "", "\n", "").Replace(content)
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	s.addMarketplaceInput += content
	s.clearMessage()
	return nil
}

func (s *PluginSelector) handleAddMarketplaceKeypress(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		s.goBack()
		return nil
	case "enter":
		return s.addMarketplace()
	case "backspace":
		if len(s.addMarketplaceInput) > 0 {
			s.addMarketplaceInput = s.addMarketplaceInput[:len(s.addMarketplaceInput)-1]
			s.clearMessage()
		}
		return nil
	default:
		if text := key.Key().Text; text != "" {
			s.addMarketplaceInput += text
			s.clearMessage()
		}
		return nil
	}
}

func (s *PluginSelector) handleDetailKeypress(key tea.KeyMsg) tea.Cmd {
	if s.handleNavigationKey(key, true) {
		return nil
	}
	switch key.String() {
	case "enter":
		return s.executeAction()
	case "esc", "left", "h":
		s.goBack()
	}
	return nil
}

func (s *PluginSelector) handleBrowseKeypress(key tea.KeyMsg) tea.Cmd {
	if s.handleNavigationKey(key, true) {
		return nil
	}
	switch key.String() {
	case "enter":
		if s.selectedIdx < len(s.browsePlugins) {
			p := s.browsePlugins[s.selectedIdx]
			s.detailDiscover = &p
			s.actions = s.buildDiscoverActions(p)
			s.actionIdx = 0
			s.level = pluginLevelDetail
		}
	case "esc", "left":
		s.goBack()
	}
	return nil
}

func (s *PluginSelector) handleNavigationKey(key tea.KeyMsg, vimKeys bool) bool {
	switch key.String() {
	case "up", "ctrl+p":
		s.MoveUp()
		return true
	case "down", "ctrl+n":
		s.MoveDown()
		return true
	case "k":
		if vimKeys {
			s.MoveUp()
			return true
		}
	case "j":
		if vimKeys {
			s.MoveDown()
			return true
		}
	}
	return false
}

func (s *PluginSelector) handleListKeypress(key tea.KeyMsg) tea.Cmd {
	if s.searchQuery == "" {
		switch key.String() {
		case "tab", "right":
			s.NextTab()
			return nil
		case "shift+tab", "left":
			s.PrevTab()
			return nil
		}
	}

	if s.handleNavigationKey(key, s.searchQuery == "") {
		return nil
	}

	switch key.String() {
	case "enter":
		s.enterDetail()
		return nil
	case "esc":
		if s.searchQuery != "" {
			s.searchQuery = ""
			s.updateFilter()
			return nil
		}
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	case "backspace":
		if len(s.searchQuery) > 0 {
			s.searchQuery = s.searchQuery[:len(s.searchQuery)-1]
			s.updateFilter()
		}
		return nil
	default:
		if text := key.Key().Text; text != "" {
			return s.handleListRuneKey(text)
		}
	}
	return nil
}

func (s *PluginSelector) handleListRuneKey(r string) tea.Cmd {
	if s.searchQuery == "" {
		switch r {
		case "l":
			s.enterDetail()
			return nil
		case " ":
			return s.toggleSelectedPlugin()
		case "u":
			return s.handleMarketplaceAction(func(m pluginMarketplaceItem) tea.Cmd {
				return s.syncMarketplace(m.ID)
			})
		case "r":
			return s.handleMarketplaceAction(func(m pluginMarketplaceItem) tea.Cmd {
				return func() tea.Msg { return pluginMarketplaceRemoveMsg{ID: m.ID} }
			})
		}
	}
	s.searchQuery += r
	s.updateFilter()
	return nil
}

func (s *PluginSelector) handleMarketplaceAction(action func(pluginMarketplaceItem) tea.Cmd) tea.Cmd {
	if s.activeTab != pluginTabMarketplaces || s.selectedIdx == 0 {
		return nil
	}
	mktIdx := s.selectedIdx - 1
	if mktIdx < len(s.filteredItems) {
		if m, ok := s.filteredItems[mktIdx].(pluginMarketplaceItem); ok {
			return action(m)
		}
	}
	return nil
}
