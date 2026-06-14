// Provider selector: model and provider selection.
package selector

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
)

func (s *Provider) Select() tea.Cmd {
	// On the Models tab: once the user has explicitly marked a model with
	// Space, Enter confirms that marked model regardless of cursor position.
	// Without an explicit mark, fall through to the highlighted row so that
	// plain navigation + Enter and search + Enter still select what the cursor
	// is on (the active model is shown [*] on open, but that is not a mark).
	if s.activeTab == providerTabModels && s.modelMarked {
		if cmd := s.selectMarkedModel(); cmd != nil {
			return cmd
		}
	}

	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visibleItems) {
		return nil
	}

	item := s.visibleItems[s.selectedIdx]
	switch item.Kind {
	case providerItemModel:
		return s.selectModel(item.Model)
	case providerItemProvider:
		return s.selectProvider(item)
	case providerItemAuthMethod:
		return s.selectAuthMethod(item)
	default:
		return nil
	}
}

func (s *Provider) selectModel(m *providerModelItem) tea.Cmd {
	if m == nil {
		return nil
	}
	s.active = false
	return func() tea.Msg {
		return ProviderModelSelectedMsg{
			ModelID:      m.ID,
			ProviderName: m.ProviderName,
			AuthMethod:   m.AuthMethod,
		}
	}
}

// selectModelFromIDs is like selectModel but takes the model identity as strings
// and constructs the message directly, without requiring a model pointer.
func (s *Provider) selectModelFromIDs(id, provider string, auth llm.AuthMethod) tea.Cmd {
	s.active = false
	return func() tea.Msg {
		return ProviderModelSelectedMsg{
			ModelID:      id,
			ProviderName: provider,
			AuthMethod:   auth,
		}
	}
}

// selectMarkedModel confirms the model the user marked with Space (the one
// rendered [*]). Used by Select() when an explicit mark exists, so the choice
// does not depend on cursor position. Returns nil if nothing is marked.
func (s *Provider) selectMarkedModel() tea.Cmd {
	for _, m := range s.allModels {
		if m.IsCurrent {
			return s.selectModelFromIDs(m.ID, m.ProviderName, m.AuthMethod)
		}
	}
	return nil
}

// toggleModel marks the currently highlighted model item (radio-style: marking
// one clears the others). Unlike Select (Enter), it only updates the IsCurrent
// flag visually and does NOT activate the model or close the overlay; the mark
// is what a subsequent Enter confirms.
func (s *Provider) toggleModel() tea.Cmd {
	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visibleItems) {
		return nil
	}
	item := s.visibleItems[s.selectedIdx]
	if item.Kind != providerItemModel || item.Model == nil {
		return nil
	}
	m := item.Model
	for i := range s.allModels {
		s.allModels[i].IsCurrent = s.allModels[i].ID == m.ID && s.allModels[i].ProviderName == m.ProviderName
	}
	for i := range s.filteredModels {
		s.filteredModels[i].IsCurrent = s.filteredModels[i].ID == m.ID && s.filteredModels[i].ProviderName == m.ProviderName
	}
	for i := range s.visibleItems {
		if s.visibleItems[i].Kind == providerItemModel && s.visibleItems[i].Model != nil {
			vi := s.visibleItems[i].Model
			vi.IsCurrent = vi.ID == m.ID && vi.ProviderName == m.ProviderName
		}
	}
	s.modelMarked = true
	return nil
}

// selectProvider handles Enter on a provider row (Providers tab).
// Connected single auth method: refresh models.
// Disconnected single auth method: auto-connect or show API key input.
// Multiple auth methods: expand inline to show auth method list.
func (s *Provider) selectProvider(item providerListItem) tea.Cmd {
	if item.Provider == nil {
		return nil
	}
	p := item.Provider

	if len(p.AuthMethods) == 1 {
		am := p.AuthMethods[0]
		if am.Status == llm.StatusConnected {
			// Refresh: re-fetch models for this connected provider
			return s.refreshAuthMethod(am, s.selectedIdx)
		}
		return s.tryConnectOrPromptKey(am, item.ProviderIdx, 0)
	}

	if len(p.AuthMethods) == 0 {
		return nil
	}

	// Multiple auth methods: toggle inline expansion
	if s.expandedProviderIdx == item.ProviderIdx {
		s.expandedProviderIdx = -1
	} else {
		s.expandedProviderIdx = item.ProviderIdx
	}
	s.resetConnectionResult()
	s.rebuildVisibleItems()
	return nil
}

func (s *Provider) selectAuthMethod(item providerListItem) tea.Cmd {
	if item.AuthMethod == nil {
		return nil
	}
	am := item.AuthMethod

	if am.Status == llm.StatusConnected {
		// Refresh: re-fetch models for this connected auth method
		return s.refreshAuthMethod(*am, s.selectedIdx)
	}

	return s.tryConnectOrPromptKey(*am, item.ProviderIdx, s.findAuthMethodIndex(item))
}

func (s *Provider) findAuthMethodIndex(item providerListItem) int {
	if item.AuthMethod == nil || item.ProviderIdx < 0 || item.ProviderIdx >= len(s.allProviders) {
		return 0
	}
	p := &s.allProviders[item.ProviderIdx]
	for i, am := range p.AuthMethods {
		if am.Provider == item.AuthMethod.Provider && am.AuthMethod == item.AuthMethod.AuthMethod {
			return i
		}
	}
	return 0
}

// SetModel sets the current model.
func (s *Provider) SetModel(modelID string, providerName string, authMethod llm.AuthMethod) (string, error) {
	if s.store == nil {
		store, err := llm.NewStore()
		if err != nil {
			return "", fmt.Errorf("failed to load store: %w", err)
		}
		s.store = store
	}

	if err := s.store.SetCurrentModel(modelID, llm.Name(providerName), authMethod); err != nil {
		return "", fmt.Errorf("failed to set model: %w", err)
	}

	return fmt.Sprintf("Model set to: %s (%s)", modelID, providerName), nil
}
