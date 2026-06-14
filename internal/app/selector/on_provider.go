// Provider selector: overlay state and message routing for the unified
// model & provider selection overlay.
//
// The selector itself (Provider) is split across sibling files by
// concern:
//
//	on_provider_types.go        data model, messages, status-display helpers
//	on_provider_nav.go          cursor/tab navigation, model search, key routing
//	on_provider_select.go       model & provider selection
//	on_provider_credentials.go  API-key entry, credential edit/remove
//	on_provider_data.go         data loading, visible-list building, lifecycle
//	on_provider_connect.go      connect/refresh flow and the in-flight spinner
//	on_provider_view.go         rendering
package selector

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
)

// ProviderState holds provider UI state for the TUI model.
// Domain state (LLM, Store, CurrentModel, tokens, thinking) lives
// on the parent app model, not here.
type ProviderState struct {
	FetchingLimits bool
	Selector       Provider
	StatusMessage  string // Temporary status shown in status bar
	statusToken    int64
}

// SetStatusMessage sets the temporary status message displayed in the status bar.
func (s *ProviderState) SetStatusMessage(msg string) int64 {
	s.statusToken++
	s.StatusMessage = msg
	return s.statusToken
}

// ProviderStatusExpiredMsg is an alias for kit.StatusExpiredMsg.
type ProviderStatusExpiredMsg = kit.StatusExpiredMsg

// UpdateProvider routes provider connection and selection messages.
func UpdateProvider(rt Runtime, state *ProviderState, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ProviderConnectingMsg:
		// Keep ticking the spinner while connect/refresh is in flight; stop once
		// the matching ProviderConnectResultMsg lands and IsConnecting goes false.
		if state.Selector.IsConnecting() {
			state.Selector.AdvanceSpinner()
			return providerConnectingTickCmd(), true
		}
		return nil, true
	case ProviderConnectResultMsg:
		return state.Selector.HandleConnectResult(msg), true
	case ProviderModelSelectedMsg:
		return handleProviderModelSelected(rt, state, msg), true
	case ProviderModelsLoadedMsg:
		state.Selector.HandleModelsLoaded(msg)
		return nil, true
	case ProviderStatusExpiredMsg:
		if msg.Token == state.statusToken {
			state.StatusMessage = ""
		}
		return nil, true
	}
	return nil, false
}

func handleProviderModelSelected(rt Runtime, state *ProviderState, msg ProviderModelSelectedMsg) tea.Cmd {
	_, err := state.Selector.SetModel(msg.ModelID, msg.ProviderName, msg.AuthMethod)
	if err != nil {
		rt.Conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: "Error: " + err.Error()})
		return tea.Batch(rt.CommitMessages()...)
	}

	rt.SetCurrentModel(&llm.CurrentModelInfo{
		ModelID:    msg.ModelID,
		Provider:   llm.Name(msg.ProviderName),
		AuthMethod: msg.AuthMethod,
	})
	ctx := context.Background()
	providerRefreshConnection(rt, state, ctx, llm.Name(msg.ProviderName), msg.AuthMethod)
	return rt.PrintWelcome(msg.ModelID)
}

func providerRefreshConnection(rt Runtime, state *ProviderState, ctx context.Context, providerName llm.Name, authMethod llm.AuthMethod) {
	p, err := llm.GetProvider(ctx, providerName, authMethod)
	if err != nil {
		log.Logger().Warn("failed to refresh provider connection",
			zap.String("provider", string(providerName)),
			zap.Error(err))
		return
	}
	rt.SwitchProvider(p)
}
