// Methods on *model that exist for sub-features (input overlay, prompt
// suggestion, trigger) to consume. Most build the Deps struct each
// sub-feature declares; a few expose model state (spinner tick, cron
// queue reset) or actions (external editor) the sub-features need.
// Centralized here so update.go / model.go stay focused on the main loop.
package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/trigger"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func (m *model) promptSuggestionDeps() input.PromptSuggestionDeps {
	return input.PromptSuggestionDeps{
		Input:        &m.userInput,
		Conversation: &m.conv.ConversationModel,
		HasProvider:  m.env.LLMProvider != nil,
		BuildClient:  m.buildLLMClient,
	}
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
			m.env.LoadThinkingEffortFromStore()
		},
		PrintWelcome: func(modelID string) tea.Cmd {
			modelName := modelID
			if m.services.LLM != nil {
				if name := m.services.LLM.Store().CachedModelDisplayName(modelID); name != "" {
					modelName = name
				}
			}
			teal := lipgloss.NewStyle().Foreground(welcomeTeal).Bold(true)
			star := lipgloss.NewStyle().Foreground(welcomeStar)
			dim := lipgloss.NewStyle().Foreground(welcomeDim)
			line := teal.Render("< ") + teal.Render("SAN") + " " + star.Render("✦") + " " + teal.Render("/>")
			if proj := projectName(m.env.CWD); proj != "" {
				line += dim.Render("  ·  ") + dim.Render(proj)
			}
			if modelName != "" {
				line += dim.Render("  ·  ") + dim.Render(modelName)
			}
			// Overwrite the original welcome line in-place using ANSI cursor
			// codes. The original printWelcome output is:
			//   \n  (leading newline from renderWelcome)
			//   <content>  (the styled welcome line)
			//   \n  (trailing newline from fmt.Println)
			// Move up 2 lines to the content line, clear it, rewrite, then
			// position the cursor on the blank line below for the TUI.
			return tea.Println("\033[2A\033[2K\r" + line + "\n")
		},
		ClearCachedInstructions: m.env.ClearCachedInstructions,
		RefreshMemoryContext:    m.refreshMemoryContext,
		FireFileChanged:         m.fireFileChanged,
		ReloadPluginState:       m.ReloadPluginBackedState,
		LoadSession:             m.loadSessionByID,
		SetActivePersona:        m.setActivePersona,
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
