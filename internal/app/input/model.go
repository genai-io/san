package input

import (
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/kit/history"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/core"
	coremcp "github.com/genai-io/san/internal/mcp"
	corepersona "github.com/genai-io/san/internal/persona"
	coreplugin "github.com/genai-io/san/internal/plugin"
	coresetting "github.com/genai-io/san/internal/setting"
	coreskill "github.com/genai-io/san/internal/skill"
)

type PastedChunk struct {
	Text      string // the full pasted text
	LineCount int    // total line count
}

type HistoryNav struct {
	Items   []string
	Index   int    // -1 = not navigating
	Stashed string // stashed textarea input while navigating
}

type Model struct {
	Textarea         textarea.Model
	History          HistoryNav
	PromptSuggestion PromptSuggestionState
	Suggestions      suggest.State
	LastCtrlO        time.Time
	LastCtrlC        time.Time
	Images           ImageState
	TerminalHeight   int
	PastedChunks     []PastedChunk
	Queue            Queue

	// Overlay modals. Each field is a full-screen overlay the input area can
	// hand control to. Two shapes, by convention:
	//
	//	XxxSelector — a self-contained list picker (cursor + render only).
	//	XxxState    — a selector wrapped with ambient state the app/runtime
	//	              needs around it: a pending invocation, an in-flight
	//	              external-editor handoff, or a status line. The picker
	//	              itself is always the embedded .Selector field.
	//
	// Approval is the odd one out: a permission confirm dialog, not a picker.
	Approval ApprovalModel

	// Self-contained selectors.
	Agent   AgentSelector
	Persona PersonaSelector
	Search  SearchSelector
	Plugin  PluginSelector
	Tool    ToolSelector
	Config  ConfigSelector

	// Selectors carrying ambient state (the picker is the .Selector field).
	Skill    SkillState    // + pending skill invocation
	Session  SessionState  // + pending-open flag
	Memory   MemoryState   // + in-flight editor file
	MCP      MCPState      // + in-flight editor file/server/scope
	Provider ProviderState // + fetching / status-line state
}

type PendingImage struct {
	ID   int
	Data core.Image
}

type ImageSelection struct {
	Active       bool
	PendingIdx   int
	CursorAbsPos int
}

type ImageState struct {
	Pending   []PendingImage
	NextID    int
	Selection ImageSelection
}

func (img *ImageState) RemoveAt(idx int) {
	if idx < 0 || idx >= len(img.Pending) {
		return
	}
	img.Pending = append(img.Pending[:idx], img.Pending[idx+1:]...)
	if len(img.Pending) == 0 {
		img.Selection = ImageSelection{}
		return
	}
	if img.Selection.PendingIdx == idx {
		img.Selection = ImageSelection{}
		return
	}
	if img.Selection.PendingIdx > idx {
		img.Selection.PendingIdx--
	}
}

type SelectorDeps struct {
	AgentRegistry   AgentRegistry
	PersonaRegistry *corepersona.Registry
	SkillRegistry   *coreskill.Registry
	MCPRegistry     *coremcp.Registry
	PluginRegistry  *coreplugin.Registry
	Setting         *coresetting.Settings
	LoadDisabled    func(userLevel bool) map[string]bool
	UpdateDisabled  func(disabled map[string]bool, userLevel bool) error
}

func New(cwd string, width int, matchFunc suggest.Matcher, deps SelectorDeps) Model {
	suggestions := suggest.NewState(matchFunc)
	suggestions.SetCwd(cwd)
	return Model{
		Textarea:    newTextarea(width),
		History:     HistoryNav{Items: history.Load(cwd), Index: -1},
		Suggestions: suggestions,
		Queue:       NewQueue(),

		Approval: NewApproval(),
		Agent:    NewAgentSelector(deps.AgentRegistry),
		Persona:  NewPersonaSelector(deps.PersonaRegistry, deps.Setting),
		Search:   NewSearchSelector(deps.Setting),
		Skill:    SkillState{Selector: NewSkillSelector(deps.SkillRegistry)},
		Session:  SessionState{Selector: NewSessionSelector()},
		Memory:   MemoryState{Selector: NewMemorySelector()},
		MCP:      MCPState{Selector: NewMCPSelector(deps.MCPRegistry)},
		Plugin:   NewPluginSelector(deps.PluginRegistry),
		Provider: ProviderState{Selector: NewProviderSelector()},
		Tool:     NewToolSelector(deps.LoadDisabled, deps.UpdateDisabled),
		Config:   NewConfigSelector(deps.Setting),
	}
}

func newTextarea(width int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetWidth(width)
	ta.SetHeight(minTextareaHeight)
	ta.ShowLineNumbers = false
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Focused.Prompt = lipgloss.NewStyle()
	styles.Blurred.Base = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	ta.SetStyles(styles)
	ta.KeyMap.InsertNewline.SetEnabled(true)
	return ta
}
