package input

import (
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/app/kit/history"
	"github.com/genai-io/san/internal/app/kit/suggest"
	"github.com/genai-io/san/internal/app/selector"
	"github.com/genai-io/san/internal/core"
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

	// selector.State is embedded so the modal selectors read as flat fields
	// (m.MCP, m.Skill.Selector, …) — the same shape they had before being
	// extracted — while the aggregate type stays in the selector package so
	// its update handlers never import input.
	selector.State
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

func New(cwd string, width int, matchFunc suggest.Matcher, deps selector.Registries) Model {
	suggestions := suggest.NewState(matchFunc)
	suggestions.SetCwd(cwd)
	return Model{
		Textarea:    newTextarea(width),
		History:     HistoryNav{Items: history.Load(cwd), Index: -1},
		Suggestions: suggestions,
		Queue:       NewQueue(),
		State:       selector.NewState(deps),
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
