package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/llm"
)

// PanelContext gives panel handlers the current input state and workspace actions.
type PanelContext struct {
	State *Model
	Conv  *chat.ConversationModel
	Cwd   string

	CommitMessages    func() []tea.Cmd
	CommitAllMessages func() []tea.Cmd

	// SwitchProvider stops the runtime before committing selection state, then
	// installs and configures the provider using the committed model.
	SwitchProvider          func(llm.Provider, func() error) error
	SetCurrentModel         func(*llm.CurrentModelInfo)
	ReloadModelStore        func()
	ClearCachedInstructions func()
	RefreshMemoryContext    func(cwd, reason string)
	FireFileChanged         func(path, tool string)
	ReloadAfterPluginChange func() error
	LoadSession             func(string) error
	SetActivePersona        func(name string) error
	OpenPersona             func(name string) tea.Cmd
	DeletePersona           func(name string) error
}
