// Provider selector: API-key entry, credential editing/removal, and the
// connect/refresh flow with its in-flight spinner.
package input

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/secret"
)

// HandlePaste inserts bracketed-paste content into the API key input when it's active.
func (s *ProviderSelector) HandlePaste(content string) tea.Cmd {
	if !s.apiKeyActive {
		return nil
	}
	content = strings.NewReplacer("\r", "", "\n", "").Replace(content)
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	s.apiKeyInput.SetValue(content)
	s.apiKeyInput.CursorEnd()
	return nil
}

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

	var am *providerAuthMethodItem
	switch item.Kind {
	case providerItemProvider:
		if item.Provider == nil || len(item.Provider.AuthMethods) != 1 {
			return nil
		}
		am = &item.Provider.AuthMethods[0]
	case providerItemAuthMethod:
		if item.AuthMethod == nil {
			return nil
		}
		am = item.AuthMethod
	default:
		return nil
	}

	// Interactive-login auth has no env var, but its stored OAuth tokens are
	// still removable, so allow the confirm prompt for either credential kind.
	envVar := providerFirstEnvVar(am.EnvVars)
	if envVar == "" && !llm.SupportsInteractiveLogin(am.Provider, am.AuthMethod) {
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

	// Clear the credential: OAuth tokens for interactive-login auth, otherwise
	// the API-key env var in the secret store.
	if llm.SupportsInteractiveLogin(providerName, authMethod) {
		_ = llm.Logout(providerName, authMethod)
	} else {
		if store := secret.Default(); store != nil {
			_ = store.Delete(envVar)
		}
		os.Unsetenv(envVar)
	}

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
	// Interactive (OAuth) auth signs in via the browser, not an API key, so it
	// never prompts for one and must sign in before it can be treated connected.
	if llm.SupportsInteractiveLogin(am.Provider, am.AuthMethod) {
		return s.connectInteractive(am, authIdx)
	}

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

// providerSpinnerInterval is the spin cadence while a connect/refresh runs —
// fast enough to read as a smooth spinner (independent of the slower global
// thinking-spinner tick).
const providerSpinnerInterval = 90 * time.Millisecond

// providerConnectingTickMsg is the periodic "still connecting/refreshing" tick that
// advances the in-flight spinner; the terminal counterpart to
// providerConnectResultMsg, which signals the work is done.
type providerConnectingTickMsg struct{}

// providerConnectingTickCmd schedules the next connecting tick (spinner frame).
func providerConnectingTickCmd() tea.Cmd {
	return tea.Tick(providerSpinnerInterval, func(time.Time) tea.Msg {
		return providerConnectingTickMsg{}
	})
}

// AdvanceSpinner moves the in-flight spinner to its next frame.
func (s *ProviderSelector) AdvanceSpinner() { s.spinnerTick++ }

// Transient in-flight result markers. While lastConnectResult equals one of
// these, the row shows an animated spinner instead of static text.
const (
	providerStatusRefreshing = "Refreshing..."
	providerStatusConnecting = "Connecting..."
)

// IsConnecting reports whether a connect/refresh is in flight, so the spinner-tick
// loop keeps ticking and the row renders an animated frame.
func (s *ProviderSelector) IsConnecting() bool {
	return s.active &&
		(s.lastConnectResult == providerStatusRefreshing || s.lastConnectResult == providerStatusConnecting)
}

// refreshAuthMethod re-fetches models for an already connected provider auth method.
func (s *ProviderSelector) refreshAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
	if s.IsConnecting() {
		// A connect/refresh is already in flight; ignore re-entry so we don't
		// start a second spinner-tick loop or a concurrent store write.
		return nil
	}
	s.lastConnectResult = providerStatusRefreshing
	s.lastConnectAuthIdx = authIdx
	s.lastConnectSuccess = false

	work := func() tea.Msg {
		ctx := context.Background()

		llmProvider, err := llm.GetProvider(ctx, item.Provider, item.AuthMethod)
		if err != nil {
			return providerConnectResultMsg{
				AuthIdx:    authIdx,
				Success:    false,
				Message:    fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
				Provider:   item.Provider,
				AuthMethod: item.AuthMethod,
			}
		}

		models, err := llmProvider.ListModels(ctx)

		store, _ := llm.NewStore()
		if store != nil && len(models) > 0 {
			_ = store.CacheModels(item.Provider, item.AuthMethod, models)
		}

		if err != nil && len(models) == 0 {
			return providerConnectResultMsg{
				AuthIdx:    authIdx,
				Success:    false,
				Message:    fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
				Provider:   item.Provider,
				AuthMethod: item.AuthMethod,
			}
		}

		if err != nil {
			return providerConnectResultMsg{
				AuthIdx:    authIdx,
				Success:    true,
				Message:    fmt.Sprintf("⚠ %d models loaded with refresh warning", len(models)),
				NewStatus:  llm.StatusConnected,
				Provider:   item.Provider,
				AuthMethod: item.AuthMethod,
				Models:     models,
			}
		}

		return providerConnectResultMsg{
			AuthIdx:    authIdx,
			Success:    true,
			Message:    fmt.Sprintf("● %d models", len(models)),
			NewStatus:  llm.StatusConnected,
			Provider:   item.Provider,
			AuthMethod: item.AuthMethod,
			Models:     models,
		}
	}
	// Start the spinner alongside the async work.
	return tea.Batch(providerConnectingTickCmd(), work)
}

// connectInteractive runs an OAuth (PKCE) sign-in for an auth method that
// authenticates in the browser, then records the connection. It reuses the
// connect spinner and result plumbing so the row animates while the browser
// flow is in progress.
func (s *ProviderSelector) connectInteractive(item providerAuthMethodItem, authIdx int) tea.Cmd {
	if s.IsConnecting() {
		// A connect/refresh is already in flight; ignore re-entry.
		return nil
	}
	s.lastConnectResult = providerStatusConnecting
	s.lastConnectAuthIdx = authIdx
	s.lastConnectSuccess = false

	work := func() tea.Msg {
		ctx := context.Background()

		// Log the authorize URL so it's recoverable when the browser can't be
		// opened automatically (e.g. over SSH).
		onURL := func(u string) {
			log.Logger().Info("provider sign-in",
				zap.String("provider", string(item.Provider)), zap.String("url", u))
		}
		if err := llm.Login(ctx, item.Provider, item.AuthMethod, onURL); err != nil {
			return providerConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: fmt.Sprintf("sign-in failed: %s", err.Error()),
			}
		}

		result, err := s.ConnectProvider(ctx, item.Provider, item.AuthMethod)
		if err != nil {
			return providerConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: err.Error(),
			}
		}
		return providerConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   result,
			NewStatus: llm.StatusConnected,
		}
	}
	return tea.Batch(providerConnectingTickCmd(), work)
}

// connectAuthMethod initiates an async connection to a provider auth method.
func (s *ProviderSelector) connectAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
	if s.IsConnecting() {
		// A connect/refresh is already in flight; ignore re-entry so we don't
		// start a second spinner-tick loop or a concurrent store write.
		return nil
	}
	s.lastConnectResult = providerStatusConnecting
	s.lastConnectAuthIdx = authIdx
	s.lastConnectSuccess = false

	work := func() tea.Msg {
		ctx := context.Background()
		result, err := s.ConnectProvider(ctx, item.Provider, item.AuthMethod)
		if err != nil {
			return providerConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: err.Error(),
			}
		}

		return providerConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   result,
			NewStatus: llm.StatusConnected,
		}
	}
	return tea.Batch(providerConnectingTickCmd(), work)
}

// HandleConnectResult updates the selector state with connection result.
func (s *ProviderSelector) HandleConnectResult(msg providerConnectResultMsg) tea.Cmd {
	s.lastConnectAuthIdx = msg.AuthIdx
	s.lastConnectResult = msg.Message
	s.lastConnectSuccess = msg.Success

	if !msg.Success {
		return nil
	}

	if len(msg.Models) > 0 {
		s.replaceModelsForAuthMethod(msg.Provider, msg.AuthMethod, msg.Models)
		s.rebuildVisibleItems()
		return nil
	}

	// Reload provider/model data, preserving UI state (tab, expansion, result).
	cmd, _ := s.loadProviderData()
	s.rebuildVisibleItems()
	return cmd
}

// ConnectProvider connects to a provider and verifies the connection.
func (s *ProviderSelector) ConnectProvider(ctx context.Context, p llm.Name, authMethod llm.AuthMethod) (string, error) {
	if s.store == nil {
		store, err := llm.NewStore()
		if err != nil {
			return "", fmt.Errorf("failed to load store: %w", err)
		}
		s.store = store
	}

	meta, ok := llm.GetMeta(p, authMethod)
	if !ok {
		return "", fmt.Errorf("provider not found: %s:%s", p, authMethod)
	}

	if !llm.IsReady(meta) {
		missingVars := []string{}
		for _, envVar := range meta.EnvVars {
			if envVar == "" {
				continue
			}
			missingVars = append(missingVars, envVar)
		}
		return "", fmt.Errorf("missing required environment variables: %s", strings.Join(missingVars, ", "))
	}

	llmProvider, err := llm.GetProvider(ctx, p, authMethod)
	if err != nil {
		return "", fmt.Errorf("failed to create provider: %w", err)
	}

	models, listErr := llmProvider.ListModels(ctx)
	if listErr != nil && len(models) == 0 {
		return "", fmt.Errorf("failed to load models for %s: %w", meta.DisplayName, listErr)
	}
	if len(models) > 0 {
		_ = s.store.CacheModels(p, authMethod, models)
	}

	if err := s.store.Connect(p, authMethod); err != nil {
		return "", fmt.Errorf("failed to save connection: %w", err)
	}

	if listErr != nil {
		return fmt.Sprintf("Connected to %s via %s (%d models; refresh warning: %v)", meta.DisplayName, authMethod, len(models), listErr), nil
	}

	return fmt.Sprintf("Connected to %s via %s (%d models)", meta.DisplayName, authMethod, len(models)), nil
}
