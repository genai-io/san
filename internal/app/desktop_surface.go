// Desktop surface wiring: the seam between the root model and the alt-screen
// reader (internal/app/desktop). The desktop renders the same conversation
// transcript and input as the inline view — reusing renderTranscriptAt and the
// inline footer — but full-screen and scrollable, so the whole history stays in
// the managed frame. This file owns the Surface enum, the toggle, the per-frame
// content snapshot, and key/mouse routing while the desktop is active.
package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/desktop"
)

// Surface selects which presentation the root model renders.
type Surface int

const (
	// SurfaceInline is the default: the live tail renders inline and finished
	// output is committed to the terminal's native scrollback.
	SurfaceInline Surface = iota
	// SurfaceDesktop is the opt-in full-screen alt-screen reader: the same view,
	// but San owns the whole screen and the full history scrolls within it.
	SurfaceDesktop
)

// enterDesktop switches to the desktop, builds its first frame, and starts the
// repaint heartbeat — but only one tick chain, so repeated toggles don't stack
// heartbeats (the chain dies in Update when the surface goes inline).
func (m *model) enterDesktop() tea.Cmd {
	m.env.Surface = SurfaceDesktop
	m.syncDesktop()
	if m.desktopTicking {
		return nil
	}
	m.desktopTicking = true
	return desktop.Tick()
}

// desktopFlushMsg is posted by exitDesktop and handled one loop iteration later,
// after the inline View has repainted (and the renderer has left the alt-screen)
// — so the backlog commit's tea.Println lands in the inline buffer, not on top
// of the alt-screen.
type desktopFlushMsg struct{}

// exitDesktop returns to the inline surface and schedules the backlog flush.
func (m *model) exitDesktop() tea.Cmd {
	m.env.Surface = SurfaceInline
	return func() tea.Msg { return desktopFlushMsg{} }
}

// syncDesktop reconciles the desktop's size, transcript, and input bar with the
// live model. Cheap to call every frame: the transcript is cached behind a
// signature inside the desktop, so the heavy render runs only on real change.
func (m *model) syncDesktop() {
	m.desktop.Resize(m.env.Width, m.env.Height)
	m.desktop.SetFooter(m.desktopFooter())
	m.desktop.SetContent(desktop.Pane{
		ID:     "conversation",
		Sig:    m.desktopSig(),
		Render: func(w, _ int) string { return m.renderTranscriptAt(w) },
	})
}

// desktopFooter is the same footer the inline view draws (separator, input,
// suggestions, status), so the two surfaces read identically.
func (m *model) desktopFooter() string {
	separator := conv.SeparatorStyle.Render(strings.Repeat("─", m.env.Width))
	return m.renderFooter(separator)
}

func (m *model) desktopSig() string {
	lastContent, lastThinking := 0, 0
	if n := len(m.conv.Messages); n > 0 {
		lastContent = len(m.conv.Messages[n-1].Content)
		lastThinking = len(m.conv.Messages[n-1].Thinking)
	}
	return fmt.Sprintf("%d|%d|%d|%d",
		len(m.conv.Messages), m.conv.CommittedCount, lastContent, lastThinking)
}

// renderTranscriptAt renders the whole conversation at the given width, reusing
// the inline renderers so the desktop transcript reads identically. The split
// mirrors the inline view exactly: finished history is rendered statically (as
// inline commits it to native scrollback), while the active tail goes through
// RenderActiveContent + renderChatSection — the same path that drives the live
// spinner and the running-tool animation above the inline input. It uses a
// surface-sized markdown renderer, off the live view's MDRenderer mutex.
func (m *model) renderTranscriptAt(width int) string {
	if m.desktopMD == nil || m.desktopMDWidth != width {
		m.desktopMD = conv.NewMDRenderer(width)
		m.desktopMDWidth = width
	}
	params := m.messageRenderParams()
	params.Width = width
	params.MDRenderer = m.desktopMD

	history := conv.RenderMessageRange(params, 0, params.CommittedCount, false)
	live := m.renderChatSection(conv.RenderActiveContent(params), m.renderTrackerList())

	parts := make([]string, 0, 2)
	if strings.TrimSpace(history) != "" {
		parts = append(parts, history)
	}
	if strings.TrimSpace(live) != "" {
		parts = append(parts, live)
	}
	return strings.Join(parts, "\n")
}

// ── key routing while the desktop is active ──────────────────────────────────

// handleDesktopKey routes keys while the desktop surface is active: exit,
// scrolling the transcript, and typing/submitting into the input bar (so chatting
// keeps working). It returns (cmd, true) when it consumes the key, or (nil,
// false) for ctrl+c so the global handler still quits/cancels.
func (m *model) handleDesktopKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+g", "esc":
		return m.exitDesktop(), true
	case "ctrl+c":
		return nil, false // let the global handler quit/cancel
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
		return m.desktop.Scroll(msg), true
	case "enter":
		return m.handleSubmit(), true
	}
	// Everything else types into the input bar, so chatting keeps working.
	cmd, _ := m.userInput.HandleTextareaUpdate(msg)
	return cmd, true
}
