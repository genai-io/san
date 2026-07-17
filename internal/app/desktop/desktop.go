// Package desktop implements San's full-screen surface: an alt-screen reader that
// renders the same conversation transcript and input as the inline view, but owns
// the whole screen so the entire history scrolls within the managed frame — where
// the inline view instead commits finished output to the terminal's native
// scrollback and can no longer touch it.
//
// It is deliberately chrome-light: one scrollable content viewport above the
// footer (the app's input bar). All conversation/markdown rendering stays in the
// app; the desktop just hands its Pane a width and scrolls the result.
package desktop

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Pane is the content the app supplies each frame — the conversation transcript,
// rendered at the surface width. Sig is a cheap change signature that gates the
// expensive rebuild.
type Pane struct {
	ID     string
	Sig    string
	Render func(w, h int) string
}

// TickMsg drives the reader's repaint heartbeat while the surface is active. The
// root model reschedules it (see Tick) until it leaves the desktop.
type TickMsg struct{}

const tickInterval = 80 * time.Millisecond

// Tick schedules the next repaint.
func Tick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return TickMsg{} })
}

// Manager owns the alt-screen reader: a single scrollable content viewport plus
// the footer. It is a value held by the root model.
type Manager struct {
	w, h   int
	vp     viewport.Model
	footer string
	sig    string
	cw, ch int
	ready  bool
}

func New() Manager { return Manager{vp: viewport.New()} }

func (mgr *Manager) Resize(w, h int) { mgr.w, mgr.h = w, h }

// SetFooter sets the bottom strip (the input bar), drawn below the transcript.
func (mgr *Manager) SetFooter(s string) { mgr.footer = s }

// SetContent syncs the scrollable transcript, rebuilding only when the signature
// or content size changes — so a markdown re-render happens on real change, not
// every frame. The reader stays pinned to the bottom when it was already there,
// so streaming output keeps the latest line in view.
func (mgr *Manager) SetContent(p Pane) {
	h := max(mgr.h-footerLines(mgr.footer), 1)
	if mgr.ready && mgr.sig == p.Sig && mgr.cw == mgr.w && mgr.ch == h {
		return
	}
	atBottom := !mgr.ready || mgr.vp.AtBottom()
	mgr.sig, mgr.cw, mgr.ch, mgr.ready = p.Sig, mgr.w, h, true
	mgr.vp.SetWidth(mgr.w)
	mgr.vp.SetHeight(h)
	mgr.vp.SetContent(p.Render(mgr.w, h))
	if atBottom {
		mgr.vp.GotoBottom()
	}
}

// Render composes the scrollable transcript above the footer — the full-screen
// counterpart of the inline view.
func (mgr *Manager) Render() string {
	if mgr.w < 1 || mgr.h < 1 {
		return ""
	}
	if footerLines(mgr.footer) > 0 {
		return lipgloss.JoinVertical(lipgloss.Left, mgr.vp.View(), mgr.footer)
	}
	return mgr.vp.View()
}

// Scroll forwards a paging key or mouse-wheel event to the transcript viewport.
func (mgr *Manager) Scroll(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	mgr.vp, cmd = mgr.vp.Update(msg)
	return cmd
}

func footerLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
