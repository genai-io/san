// Methods on *model that exist for sub-features (input overlay and prompt
// suggestion) to consume. Most build the Deps struct each
// sub-feature declares; a few expose model state (spinner tick, cron
// queue reset) or actions (external editor) the sub-features need.
// Centralized here so update.go / model.go stay focused on the main loop.
package tui

import (
	tea "charm.land/bubbletea/v2"
	"go.uber.org/zap"

	"github.com/genai-io/san/internal/app/tui/input"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/infra/log"
	"github.com/genai-io/san/internal/llm"
)

func (m *model) overlayContext() input.OverlayContext {
	reloadModelStore := func() {
		if store := m.services.LLM.Store(); store != nil {
			if err := store.Reload(); err != nil {
				log.Logger().Warn("reload provider store after model catalog update", zap.Error(err))
			}
		}
	}
	return input.OverlayContext{
		State:             &m.userInput,
		Conv:              &m.conv.ConversationModel,
		Cwd:               m.env.CWD,
		CommitMessages:    m.CommitMessages,
		CommitAllMessages: m.commitAllMessages,
		SwitchProvider: func(p llm.Provider, commitSelection func() error) error {
			if err := m.StopAgentRuntime(); err != nil {
				return err
			}
			if err := commitSelection(); err != nil {
				return err
			}
			m.switchProvider(p)
			m.ReconfigureAgentTool()
			return nil
		},
		SetCurrentModel: func(info *llm.CurrentModelInfo) {
			m.env.CurrentModel = info
			m.services.LLM.SetCurrentModel(info)
			// The selector cached the model's metadata (display name, token
			// limits) through its own Store; reload the shared store so the
			// status bar reflects the new model's name and context-window
			// limit instead of the raw ID and "--".
			reloadModelStore()
			m.env.LoadThinkingEffortFromStore()
		},
		ReloadModelStore: reloadModelStore,
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

func (m *model) StartExternalEditor(path string) tea.Cmd {
	return widgets.StartExternalEditor(path, func(err error) tea.Msg {
		return input.MemoryEditorFinishedMsg{Err: err}
	})
}

func (m *model) SpinnerTickCmd() tea.Cmd { return m.conv.Spinner.Tick }
func (m *model) ResetCronQueue()         { m.cronQueue = nil }
