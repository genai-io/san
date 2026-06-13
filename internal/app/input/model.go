package input

import (
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"

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

	// Selectors / overlays
	Approval ApprovalModel
	Agent    AgentSelector
	Persona  PersonaSelector
	Search   SearchSelector
	Skill    SkillState
	Session  SessionState
	Memory   MemoryState
	MCP      MCPState
	Plugin   PluginSelector
	Provider ProviderState
	Tool     ToolSelector
	Config   ConfigSelector
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
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
	ta.KeyMap.InsertNewline.SetEnabled(true)
	return ta
}
