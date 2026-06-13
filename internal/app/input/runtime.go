package input

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
)

// OverlayDeps holds all dependencies needed by overlay selector handlers.
type OverlayDeps struct {
	State *Model
	Conv  *conv.ConversationModel
	Cwd   string

	CommitMessages    func() []tea.Cmd
	CommitAllMessages func() []tea.Cmd

	SwitchProvider          func(llm.Provider)
	SetCurrentModel         func(*llm.CurrentModelInfo)
	PrintWelcome            func(modelID string) tea.Cmd
	ClearCachedInstructions func()
	RefreshMemoryContext    func(cwd, reason string)
	FireFileChanged         func(path, tool string)
	ReloadPluginState       func() error
	LoadSession             func(string) error
	SetActivePersona        func(name string) error
	CreatePersona           func(name string) (tea.Cmd, error)
	EditPersona             func(name string) tea.Cmd
	DeletePersona           func(name string) error
}
