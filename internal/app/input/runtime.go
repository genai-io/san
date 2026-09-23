package input

import (
	tea "charm.land/bubbletea/v2"

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
	ReloadModelStore        func()
	ClearCachedInstructions func()
	RefreshMemoryContext    func(cwd, reason string)
	FireFileChanged         func(path, tool string)
	ReloadAfterPluginChange func() error
	LoadSession             func(string) error
	SetActivePersona        func(name string) error
	OpenPersona             func(name string) tea.Cmd
	DeletePersona           func(name string) error
	// RenderHistory prepares the /history viewer's content: a title and the
	// already-rendered lines of the messages the current view left out of
	// native scrollback. Rendering needs the conv render context, which lives
	// in the app layer, so the viewer is handed lines and only scrolls them.
	RenderHistory func() (string, []string)
}
