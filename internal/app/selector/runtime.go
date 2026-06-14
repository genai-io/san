package selector

import (
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/llm"
)

// Runtime holds all dependencies needed by selector update handlers. The
// handlers reach app-level behavior only through these callbacks, so the
// selector package never imports the parent app/input packages.
type Runtime struct {
	State *State
	Conv  *conv.ConversationModel
	Cwd   string

	// SetInput writes a value into the user's textarea (e.g. prefilling
	// "/mcp add "). Replaces the former direct State.Textarea access so the
	// overlay package stays decoupled from the input model.
	SetInput func(string)

	CommitMessages    func() []tea.Cmd
	CommitAllMessages func() []tea.Cmd

	SwitchProvider          func(llm.Provider)
	SetCurrentModel         func(*llm.CurrentModelInfo)
	PrintWelcome            func(modelID string) tea.Cmd
	ClearCachedInstructions func()
	RefreshMemoryContext    func(cwd, reason string)
	FireFileChanged         func(path, tool string)
	ReloadAfterPluginChange func() error
	LoadSession             func(string) error
	SetActivePersona        func(name string) error
	OpenPersona             func(name string) tea.Cmd
	DeletePersona           func(name string) error
}
