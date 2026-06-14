// Provider selector: connect/refresh flow and the in-flight spinner.
package selector

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/llm"
)

// providerSpinnerInterval is the spin cadence while a connect/refresh runs —
// fast enough to read as a smooth spinner (independent of the slower global
// thinking-spinner tick).
const providerSpinnerInterval = 90 * time.Millisecond

// ProviderConnectingMsg is the periodic "still connecting/refreshing" tick that
// advances the in-flight spinner; the terminal counterpart to
// ProviderConnectResultMsg, which signals the work is done.
type ProviderConnectingMsg struct{}

// providerConnectingTickCmd schedules the next connecting tick (spinner frame).
func providerConnectingTickCmd() tea.Cmd {
	return tea.Tick(providerSpinnerInterval, func(time.Time) tea.Msg {
		return ProviderConnectingMsg{}
	})
}

// AdvanceSpinner moves the in-flight spinner to its next frame.
func (s *Provider) AdvanceSpinner() { s.spinnerTick++ }

// Transient in-flight result markers. While lastConnectResult equals one of
// these, the row shows an animated spinner instead of static text.
const (
	providerStatusRefreshing = "Refreshing..."
	providerStatusConnecting = "Connecting..."
)

// IsConnecting reports whether a connect/refresh is in flight, so the spinner-tick
// loop keeps ticking and the row renders an animated frame.
func (s *Provider) IsConnecting() bool {
	return s.active &&
		(s.lastConnectResult == providerStatusRefreshing || s.lastConnectResult == providerStatusConnecting)
}

// refreshAuthMethod re-fetches models for an already connected provider auth method.
func (s *Provider) refreshAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
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
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
			}
		}

		models, err := llmProvider.ListModels(ctx)

		store, _ := llm.NewStore()
		if store != nil && len(models) > 0 {
			_ = store.CacheModels(item.Provider, item.AuthMethod, models)
		}

		if err != nil && len(models) == 0 {
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: fmt.Sprintf("failed to load models for %s: %s", item.Provider, err.Error()),
			}
		}

		if err != nil {
			return ProviderConnectResultMsg{
				AuthIdx:   authIdx,
				Success:   true,
				Message:   fmt.Sprintf("⚠ %d models loaded with refresh warning", len(models)),
				NewStatus: llm.StatusConnected,
			}
		}

		return ProviderConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   fmt.Sprintf("● %d models", len(models)),
			NewStatus: llm.StatusConnected,
		}
	}
	// Start the spinner alongside the async work.
	return tea.Batch(providerConnectingTickCmd(), work)
}

// connectAuthMethod initiates an async connection to a provider auth method.
func (s *Provider) connectAuthMethod(item providerAuthMethodItem, authIdx int) tea.Cmd {
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
			return ProviderConnectResultMsg{
				AuthIdx: authIdx,
				Success: false,
				Message: err.Error(),
			}
		}

		return ProviderConnectResultMsg{
			AuthIdx:   authIdx,
			Success:   true,
			Message:   result,
			NewStatus: llm.StatusConnected,
		}
	}
	return tea.Batch(providerConnectingTickCmd(), work)
}

// HandleConnectResult updates the selector state with connection result.
func (s *Provider) HandleConnectResult(msg ProviderConnectResultMsg) tea.Cmd {
	s.lastConnectAuthIdx = msg.AuthIdx
	s.lastConnectResult = msg.Message
	s.lastConnectSuccess = msg.Success

	if !msg.Success {
		return nil
	}

	// Reload provider/model data, preserving UI state (tab, expansion, result).
	cmd, _ := s.loadProviderData()
	s.rebuildVisibleItems()
	return cmd
}

// ConnectProvider connects to a provider and verifies the connection.
func (s *Provider) ConnectProvider(ctx context.Context, p llm.Name, authMethod llm.AuthMethod) (string, error) {
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
