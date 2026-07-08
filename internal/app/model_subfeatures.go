// Methods on *model that exist for sub-features (input overlay, prompt
// suggestion, trigger) to consume. Most build the Deps struct each
// sub-feature declares; a few expose model state (spinner tick, cron
// queue reset) or actions (external editor) the sub-features need.
// Centralized here so update.go / model.go stay focused on the main loop.
package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
)

// startPromptSuggestion fires the input hint, unless AutoPilot is engaged with
// the Suggest steer off — in which case the copilot deliberately doesn't nudge.
// Both this gate and the mission below read the live autopilot config; the hint
// keeps no autopilot state of its own, so AutoPilot is the single source.
func (m *model) startPromptSuggestion() tea.Cmd {
	if m.autopilotEngaged() && !m.env.AutoPilot.Steers.Suggest {
		return nil
	}
	return input.StartPromptSuggestion(m.promptSuggestionDeps())
}

func (m *model) promptSuggestionDeps() input.PromptSuggestionDeps {
	return input.PromptSuggestionDeps{
		Input:        &m.userInput,
		Conversation: &m.conv.ConversationModel,
		HasProvider:  m.env.LLMProvider != nil,
		BuildClient:  m.buildLLMClient,
		Mission:      m.autopilotSuggestMission(),
	}
}

// autopilotSuggestMission returns the mission the Suggest steer should propose
// the next step toward — the live autopilot mission when the steer is on, else
// "" for the generic prediction. Read fresh from env every call, so clearing
// the mission (End completion / ctrl+r / panel) takes effect immediately.
func (m *model) autopilotSuggestMission() string {
	if !m.autopilotEngaged() || !m.env.AutoPilot.Steers.Suggest {
		return ""
	}
	return strings.TrimSpace(m.env.AutoPilot.Mission)
}

func (m *model) overlayDeps() input.OverlayDeps {
	return input.OverlayDeps{
		State:             &m.userInput,
		Conv:              &m.conv.ConversationModel,
		Cwd:               m.env.CWD,
		CommitMessages:    m.CommitMessages,
		CommitAllMessages: m.commitAllMessages,
		SwitchProvider: func(p llm.Provider) {
			m.StopAgentSession()
			m.switchProvider(p)
			m.ReconfigureAgentTool()
		},
		SetCurrentModel: func(info *llm.CurrentModelInfo) {
			m.env.CurrentModel = info
			llm.Default().SetCurrentModel(info)
			// The selector cached the model's metadata (display name, token
			// limits) through its own Store; reload the shared store so the
			// status bar reflects the new model's name and context-window
			// limit instead of the raw ID and "--".
			if store := m.services.LLM.Store(); store != nil {
				if err := store.Reload(); err != nil {
					log.Logger().Warn("reload provider store after model switch", zap.Error(err))
				}
			}
			m.env.LoadThinkingEffortFromStore()
		},
		// No welcome reprint on model switch: the live status line already
		// shows the current model, and the startup brand line is a one-time
		// splash (re-emitting it duplicated the banner / leaked blank lines).
		ClearCachedInstructions: m.env.ClearCachedInstructions,
		RefreshMemoryContext:    m.refreshMemoryContext,
		FireFileChanged:         m.fireFileChanged,
		ReloadAfterPluginChange: m.ReloadAfterPluginChange,
		LoadSession:             m.loadSessionByID,
		SetActivePersona:        m.setActivePersona,
		OpenPersona:             m.openPersona,
		DeletePersona:           m.deletePersona,
	}
}

func (m *model) triggerDeps() trigger.Deps {
	return trigger.Deps{
		StreamActive: m.conv.Stream.Active,
		Cron:         m.services.Cron,
		InjectCron:   m.injectCronPrompt,
		InjectHook:   m.injectAsyncHookContinuation,
		AppendNotice: func(text string) {
			if text != "" {
				m.conv.Append(core.ChatMessage{Role: core.RoleNotice, Content: text})
			}
		},
	}
}

func (m *model) StartExternalEditor(path string) tea.Cmd {
	return kit.StartExternalEditor(path, func(err error) tea.Msg {
		return input.MemoryEditorFinishedMsg{Err: err}
	})
}

func (m *model) SpinnerTickCmd() tea.Cmd { return m.conv.Spinner.Tick }
func (m *model) ResetCronQueue()         { m.systemInput.CronQueue = nil }
