package input

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/genai-io/gen-code/internal/app/kit"
	"github.com/genai-io/gen-code/internal/identity"
)

// IdentitySelector is the /identity overlay. Read-only — selects the
// active persona by writing settings. Create / edit are reached via the
// uppercase N / E hotkeys, which dispatch to the unified /identity
// slash command (/identity create | /identity edit <name>).
type IdentitySelector struct {
	registry *identity.Registry
	active   bool

	items         []*identity.Identity
	visible       []*identity.Identity
	nav           kit.ListNav
	width, height int

	// activeName mirrors settings.identity at open time, used for the ● marker.
	activeName    string
	getActiveName func() string

	// hint is a transient warning shown at the bottom (e.g. when E is pressed
	// on the built-in default).
	hint string
}

// IdentityActivateMsg is emitted when the user presses Enter on a non-active
// identity. The model handles it by writing settings.identity and rebuilding
// the agent. Empty Name = revert to built-in default.
type IdentityActivateMsg struct {
	Name string
}

// IdentitySlashMsg is emitted when Shift+N / Shift+E is pressed. The model
// dispatches the command through the normal slash-command path.
type IdentitySlashMsg struct {
	Command string // e.g. "/identity create" or "/identity edit ml-engineer"
}

// NewIdentitySelector wires the selector to its registry and a getter for
// the current settings.identity (so the ● marker reflects truth at open).
func NewIdentitySelector(reg *identity.Registry, getActive func() string) IdentitySelector {
	return IdentitySelector{
		registry:      reg,
		getActiveName: getActive,
		nav:           kit.ListNav{MaxVisible: 6},
	}
}

func (s *IdentitySelector) IsActive() bool { return s.active }

func (s *IdentitySelector) SetRegistry(reg *identity.Registry) {
	s.registry = reg
	if s.active {
		s.refresh()
	}
}

// Enter activates the selector for an interactive session.
func (s *IdentitySelector) Enter(_ context.Context, width, height int) (tea.Cmd, error) {
	s.width = width
	s.height = height
	s.hint = ""
	s.nav.Reset()
	if s.getActiveName != nil {
		s.activeName = s.getActiveName()
	}
	if s.registry != nil {
		s.registry.Reload()
	}
	s.refresh()
	s.active = true
	return nil, nil
}

func (s *IdentitySelector) Cancel() {
	s.active = false
	s.hint = ""
	s.nav.Reset()
}

// HandleKeypress dispatches keystrokes. Uppercase N/E are checked BEFORE
// search-as-you-type so they can't be consumed as filter input (lowercase
// n/k are still available for navigation/search).
func (s *IdentitySelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if !s.active {
		return nil
	}

	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		switch key.Runes[0] {
		case 'N':
			s.active = false
			return func() tea.Msg { return IdentitySlashMsg{Command: "/identity create"} }
		case 'E':
			sel := s.selectedItem()
			if sel == nil || sel.IsBuiltin() {
				s.hint = "default is built-in — press Shift+N to create your own"
				return nil
			}
			s.active = false
			return func() tea.Msg { return IdentitySlashMsg{Command: "/identity edit " + sel.Name} }
		}
	}

	if key.Type == tea.KeyEnter {
		sel := s.selectedItem()
		if sel == nil {
			return nil
		}
		// Enter on the already-active identity is a no-op close.
		if sel.Name == s.activeName || (s.activeName == "" && sel.Name == identity.DefaultName) {
			s.Cancel()
			return nil
		}
		// "default" activates as empty name (clears the override).
		name := sel.Name
		if sel.IsBuiltin() {
			name = ""
		}
		s.active = false
		return func() tea.Msg { return IdentityActivateMsg{Name: name} }
	}

	searchChanged, consumed := s.nav.HandleKey(key)
	if searchChanged {
		s.refilter()
	}
	if consumed {
		return nil
	}
	if key.Type == tea.KeyEsc {
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}
	return nil
}

func (s *IdentitySelector) refresh() {
	if s.registry == nil {
		s.items = []*identity.Identity{identity.DefaultIdentity()}
	} else {
		s.items = s.registry.List()
	}
	s.refilter()
}

func (s *IdentitySelector) refilter() {
	q := strings.ToLower(strings.TrimSpace(s.nav.Search))
	if q == "" {
		s.visible = s.items
	} else {
		out := make([]*identity.Identity, 0, len(s.items))
		for _, it := range s.items {
			if kit.FuzzyMatch(strings.ToLower(it.Name), q) ||
				kit.FuzzyMatch(strings.ToLower(it.Description), q) {
				out = append(out, it)
			}
		}
		s.visible = out
	}
	s.nav.Total = len(s.visible)
	s.nav.ResetCursor()
}

func (s *IdentitySelector) selectedItem() *identity.Identity {
	if s.nav.Selected < 0 || s.nav.Selected >= len(s.visible) {
		return nil
	}
	return s.visible[s.nav.Selected]
}
