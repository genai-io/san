// Session selector feature — flattened from sessionui package.
package input

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/session"
)

// sessionSelectedMsg is sent when a session is selected.
type sessionSelectedMsg struct {
	SessionID string
}

// SessionSelector holds the state for the session selector.
type SessionSelector struct {
	active   bool
	sessions []session.Summary
	filtered []session.Summary
	nav      kit.ListNav
	width    int
	height   int
	cwd      string

	loading    bool
	loadError  error
	generation uint64
	cancel     context.CancelFunc
}

// SessionState holds session selector UI state for the TUI model.
type SessionState struct {
	Selector        SessionSelector
	PendingSelector bool
}

// NewSessionSelector creates a new SessionSelector.
func NewSessionSelector() SessionSelector {
	return SessionSelector{nav: kit.ListNav{MaxVisible: 6}}
}

func calculateSessionMaxVisible(height int) int {
	const (
		fixedLines      = 7
		linesPerSession = 3
	)
	return max(3, min((height-fixedLines)/linesPerSession, 20))
}

func calculateSessionPreviewLength(width int) int {
	return max(30, min(width-10, 120))
}

type sessionsListedMsg struct {
	generation uint64
	sessions   []session.Summary
	err        error
}

// EnterSelect paints immediately; disk I/O runs on the returned command.
func (s *SessionSelector) EnterSelect(width, height int, store *session.Store, cwd string) (tea.Cmd, error) {
	if store == nil {
		return nil, fmt.Errorf("session store is required")
	}
	s.Cancel()
	ctx, cancel := context.WithCancel(context.Background())
	s.active, s.loading, s.cancel = true, true, cancel
	s.cwd = cwd
	s.Resize(width, height)
	generation := s.generation
	return func() tea.Msg {
		defer cancel()
		sessions, err := store.ListContext(ctx)
		return sessionsListedMsg{generation: generation, sessions: sessions, err: err}
	}, nil
}

func (s *SessionSelector) Resize(width, height int) {
	s.width, s.height = width, height
	s.nav.MaxVisible = calculateSessionMaxVisible(height)
	s.nav.EnsureVisible()
}

func (s *SessionSelector) IsActive() bool {
	return s.active
}

func (s *SessionSelector) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
	generation := s.generation + 1
	*s = NewSessionSelector()
	s.generation = generation
}

func (s *SessionSelector) updateFilter() {
	query := strings.ToLower(s.nav.Search)
	s.filtered = s.filtered[:0]

	for _, sess := range s.sessions {
		if query != "" && !kit.FuzzyMatch(strings.ToLower(sess.Title), query) &&
			!kit.FuzzyMatch(strings.ToLower(sess.Model), query) {
			continue
		}
		s.filtered = append(s.filtered, sess)
	}

	s.nav.ResetCursor()
	s.nav.Total = len(s.filtered)
}

func (s *SessionSelector) Select() tea.Cmd {
	if s.loading || len(s.filtered) == 0 || s.nav.Selected >= len(s.filtered) {
		return nil
	}

	selected := s.filtered[s.nav.Selected]
	s.active = false

	return func() tea.Msg {
		return sessionSelectedMsg{SessionID: selected.ID}
	}
}

func (s *SessionSelector) HandleKeypress(key tea.KeyMsg) tea.Cmd {
	if key.String() == "enter" {
		return s.Select()
	}

	searchChanged, consumed := s.nav.HandleKey(key)
	if searchChanged {
		s.updateFilter()
	}
	if consumed {
		return nil
	}

	if key.String() == "esc" {
		s.Cancel()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}

	return nil
}

func sessionFormatCompactMetadata(sess session.Summary) string {
	return fmt.Sprintf("%d msgs · %s", sess.MessageCount, sessionFormatRelativeTime(sess.UpdatedAt))
}

func sessionTruncateToFirstLine(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	if first, _, found := strings.Cut(content, "\n"); found {
		content = first
	}
	// maxLen is a column budget from calculateSessionPreviewLength; measuring
	// it in bytes cuts a Chinese prompt to a third of the row and can land
	// mid-rune.
	return kit.TruncateText(content, maxLen)
}

func (s *SessionSelector) renderSession(sess session.Summary, isSelected bool, sb *strings.Builder, boxWidth int) {
	// RenderSelectableRow adds a 4-char left prefix for unselected rows
	// ("  " + PaddingLeft(2)) and the same 4-char visual offset for
	// selected rows ("  ▎ "). We account for that below so the row is
	// exactly boxWidth columns wide with metadata flush right.

	displayTitle := sess.Title

	metadata := sessionFormatCompactMetadata(sess)
	mdWidth := lipgloss.Width(metadata)

	// Available width for the title: boxWidth minus RenderSelectableRow's 4-col
	// prefix, minus 1-space minimum gap, minus the metadata width.
	availTitleWidth := max(boxWidth-4-mdWidth-1, 10)
	title := kit.TruncateText(displayTitle, availTitleWidth)

	// Pad so (title + padding + metadata) is boxWidth − 4, which makes the
	// full row (including RenderSelectableRow's 4-col prefix) exactly boxWidth.
	titleWidth := lipgloss.Width(title)
	gap := max(boxWidth-4-titleWidth-mdWidth, 1)
	padding := strings.Repeat(" ", gap)
	sb.WriteString(kit.RenderSelectableRow(title+padding+metadata, isSelected) + "\n")

	if lastMsg := sessionTruncateToFirstLine(sess.LastPrompt, calculateSessionPreviewLength(s.width)); lastMsg != "" {
		previewStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Muted)
		sb.WriteString(previewStyle.Render(fmt.Sprintf("    %s", lastMsg)))
	}
	sb.WriteString("\n\n")
}

func (s *SessionSelector) Render() string {
	if !s.active {
		return ""
	}

	var sb strings.Builder

	title := fmt.Sprintf("Resume Session - %s (%d/%d)", filepath.Base(s.cwd), len(s.filtered), len(s.sessions))
	sb.WriteString(kit.SelectorTitleStyle().Render(title) + "\n")

	searchLine := "Type to filter..."
	searchStyle := kit.SelectorHintStyle()
	if s.nav.Search != "" {
		searchLine = s.nav.Search + "▏"
		searchStyle = kit.SelectorBreadcrumbStyle()
	}
	sb.WriteString(searchStyle.Render(searchLine) + "\n\n")

	if s.loading {
		sb.WriteString(kit.SelectorHintStyle().Render("  Loading sessions…") + "\n")
	} else if s.loadError != nil {
		sb.WriteString(kit.SelectorHintStyle().Render("  Failed to list sessions: "+s.loadError.Error()) + "\n")
	} else if len(s.sessions) == 0 {
		sb.WriteString(kit.SelectorHintStyle().Render("  No sessions found") + "\n")
	} else if len(s.filtered) == 0 {
		sb.WriteString(kit.SelectorHintStyle().Render("  No sessions match the filter") + "\n")
	} else {
		startIdx, endIdx := s.nav.VisibleRange()
		s.renderScrollIndicator(&sb, startIdx > 0, "↑ more above")

		for i := startIdx; i < endIdx; i++ {
			s.renderSession(s.filtered[i], i == s.nav.Selected, &sb, s.width)
		}

		s.renderScrollIndicator(&sb, endIdx < len(s.filtered), "↓ more below")
	}

	sb.WriteString("\n" + kit.SelectorHintStyle().Render("↑/↓ navigate · Enter select · Esc clear/cancel"))
	return sb.String()
}

func (s *SessionSelector) renderScrollIndicator(sb *strings.Builder, show bool, text string) {
	if show {
		sb.WriteString(kit.SelectorHintStyle().Render("  "+text) + "\n")
	}
}

func sessionFormatRelativeTime(t time.Time) string {
	diff := time.Since(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return kit.Plural(int(diff.Minutes()), "min") + " ago"
	case diff < 24*time.Hour:
		return kit.Plural(int(diff.Hours()), "hour") + " ago"
	case diff < 48*time.Hour:
		return "yesterday"
	case diff < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(diff.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

// UpdateSession routes session selection messages.
func UpdateSession(deps OverlayDeps, state *SessionState, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case sessionsListedMsg:
		selector := &state.Selector
		if selector.active && selector.generation == msg.generation {
			selector.loading, selector.cancel = false, nil
			selector.sessions, selector.loadError = msg.sessions, msg.err
			selector.updateFilter()
		}
		return nil, true
	case sessionSelectedMsg:
		return handleSessionSelected(deps, msg), true
	}
	return nil, false
}

func handleSessionSelected(deps OverlayDeps, msg sessionSelectedMsg) tea.Cmd {
	if err := deps.LoadSession(msg.SessionID); err != nil {
		deps.Conv.AddNotice("Failed to load session: " + err.Error())
	}

	// Replay the restored conversation through the normal scrollback pipeline.
	return tea.Batch(deps.CommitAllMessages()...)
}
