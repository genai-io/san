// Provider selector: API-key entry, credential editing, and removal.
package input

import (
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/secret"
)

func (s *ProviderSelector) handleAPIKeyInput(key tea.KeyMsg) tea.Cmd {
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
		s.apiKeyActive = false

		// Find the auth method and trigger connection
		if s.apiKeyProviderIdx >= 0 && s.apiKeyProviderIdx < len(s.allProviders) {
			dp := &s.allProviders[s.apiKeyProviderIdx]
			if s.apiKeyAuthIdx >= 0 && s.apiKeyAuthIdx < len(dp.AuthMethods) {
				am := dp.AuthMethods[s.apiKeyAuthIdx]
				return s.connectAuthMethod(am, s.selectedIdx)
			}
		}
		return nil

	case "esc":
		s.apiKeyActive = false
		return nil

	default:
		var cmd tea.Cmd
		s.apiKeyInput, cmd = s.apiKeyInput.Update(key)
		return cmd
	}
}

// handleCredentialEdit handles the 'e' key for editing credentials on connected providers.
// For providers with a single auth method: activates API key input directly.
// For providers with multiple auth methods: expands the provider first, then allows editing.
func (s *ProviderSelector) handleCredentialEdit() tea.Cmd {
	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visibleItems) {
		return nil
	}

	item := s.visibleItems[s.selectedIdx]

	switch item.Kind {
	case providerItemProvider:
		return s.handleCredentialEditForProvider(item)
	case providerItemAuthMethod:
		return s.handleCredentialEditForAuthMethod(item)
	default:
		return nil
	}
}

// handleCredentialEditForProvider handles credential edit for a provider row.
func (s *ProviderSelector) handleCredentialEditForProvider(item providerListItem) tea.Cmd {
	if item.Provider == nil {
		return nil
	}
	p := item.Provider

	// Single auth method: activate API key input directly
	if len(p.AuthMethods) == 1 {
		am := p.AuthMethods[0]
		envVar := providerFirstEnvVar(am.EnvVars)
		if envVar == "" {
			return nil
		}
		s.apiKeyProviderIdx = item.ProviderIdx
		s.apiKeyAuthIdx = 0
		s.initAPIKeyInput(envVar)
		return nil
	}

	// Multiple auth methods: expand if not already expanded
	if len(p.AuthMethods) == 0 {
		return nil
	}

	if s.expandedProviderIdx != item.ProviderIdx {
		s.expandedProviderIdx = item.ProviderIdx
		s.resetConnectionResult()
		s.rebuildVisibleItems()
	}

	return nil
}

// handleCredentialEditForAuthMethod handles credential edit for an auth method row.
func (s *ProviderSelector) handleCredentialEditForAuthMethod(item providerListItem) tea.Cmd {
	if item.AuthMethod == nil {
		return nil
	}
	am := item.AuthMethod

	envVar := providerFirstEnvVar(am.EnvVars)
	if envVar == "" {
		return nil
	}

	s.apiKeyProviderIdx = item.ProviderIdx
	s.apiKeyAuthIdx = s.findAuthMethodIndex(item)
	s.initAPIKeyInput(envVar)
	return nil
}

// handleCredentialRemove handles Ctrl+D: shows a confirmation prompt before removing.
func (s *ProviderSelector) handleCredentialRemove() tea.Cmd {
	if s.activeTab != providerTabProviders {
		return nil
	}
	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visibleItems) {
		return nil
	}

	item := s.visibleItems[s.selectedIdx]

	var envVars []string
	switch item.Kind {
	case providerItemProvider:
		if item.Provider == nil || len(item.Provider.AuthMethods) != 1 {
			return nil
		}
		envVars = item.Provider.AuthMethods[0].EnvVars
	case providerItemAuthMethod:
		if item.AuthMethod == nil {
			return nil
		}
		envVars = item.AuthMethod.EnvVars
	default:
		return nil
	}

	envVar := providerFirstEnvVar(envVars)
	if envVar == "" {
		return nil
	}

	s.confirmRemoveActive = true
	s.confirmRemoveEnvVar = envVar
	s.confirmRemoveItemIdx = s.selectedIdx
	return nil
}

// handleConfirmRemove handles keypresses while the confirm-remove prompt is active.
func (s *ProviderSelector) handleConfirmRemove(key tea.KeyMsg) tea.Cmd {
	s.confirmRemoveActive = false
	switch key.String() {
	case "y", "Y":
		return s.executeCredentialRemove()
	default:
		return nil
	}
}

// executeCredentialRemove performs the actual credential removal.
func (s *ProviderSelector) executeCredentialRemove() tea.Cmd {
	envVar := s.confirmRemoveEnvVar

	// Resolve the provider and auth method from the item
	item := s.visibleItems[s.confirmRemoveItemIdx]
	var providerName llm.Name
	var authMethod llm.AuthMethod
	switch item.Kind {
	case providerItemProvider:
		if item.Provider == nil || len(item.Provider.AuthMethods) != 1 {
			return nil
		}
		providerName = item.Provider.AuthMethods[0].Provider
		authMethod = item.Provider.AuthMethods[0].AuthMethod
	case providerItemAuthMethod:
		if item.AuthMethod == nil {
			return nil
		}
		providerName = item.AuthMethod.Provider
		authMethod = item.AuthMethod.AuthMethod
	default:
		return nil
	}

	// Remove from secret store and unset env var
	if store := secret.Default(); store != nil {
		_ = store.Delete(envVar)
	}
	os.Unsetenv(envVar)

	// Disconnect provider and remove cached models from the llm store
	if s.store != nil {
		_ = s.store.Disconnect(providerName)
		_ = s.store.RemoveCachedModels(providerName, authMethod)

		// Clear current model if it belongs to the disconnected provider
		if cur := s.store.GetCurrentModel(); cur != nil && cur.Provider == providerName {
			_ = s.store.ClearCurrentModel()
			llm.Default().SetCurrentModel(nil)
		}

		// If no connections remain, clear the runtime provider too
		if len(s.store.GetConnections()) == 0 {
			llm.Default().SetProvider(nil)
		}
	}

	// Reload provider data to reflect the removed credential
	s.resetConnectionResult()
	_, _ = s.loadProviderData()
	s.rebuildVisibleItems()
	return nil
}

// tryConnectOrPromptKey connects if env vars are available, otherwise shows API key input.
func (s *ProviderSelector) tryConnectOrPromptKey(am providerAuthMethodItem, providerIdx, authIdx int) tea.Cmd {
	if am.Status == llm.StatusAvailable || providerIsEnvReady(am.EnvVars) {
		return s.connectAuthMethod(am, s.selectedIdx)
	}

	// Show inline API key input
	envVar := providerFirstEnvVar(am.EnvVars)
	if envVar == "" {
		return nil
	}
	s.apiKeyProviderIdx = providerIdx
	s.apiKeyAuthIdx = authIdx
	s.initAPIKeyInput(envVar)
	return nil
}

// initAPIKeyInput initializes the textinput for API key entry.
func (s *ProviderSelector) initAPIKeyInput(envVar string) {
	ti := textinput.New()
	ti.Placeholder = envVar
	ti.Focus()
	ti.CharLimit = 256
	ti.SetWidth(40)
	ti.EchoMode = textinput.EchoPassword
	s.apiKeyInput = ti
	s.apiKeyActive = true
	s.apiKeyEnvVar = envVar
}

func providerIsEnvReady(envVars []string) bool {
	for _, v := range envVars {
		if v != "" && secret.Resolve(v) != "" {
			return true
		}
	}
	return false
}

func providerFirstEnvVar(envVars []string) string {
	for _, v := range envVars {
		if v != "" {
			return v
		}
	}
	return ""
}
