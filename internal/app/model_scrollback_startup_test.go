package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The first paint can precede the initial size message and its resize report.
type rendererStartupModel struct {
	tea.Model
	ready       chan struct{}
	initialized bool
}

func (m *rendererStartupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.Model, cmd = m.Model.Update(msg)
	if _, ok := msg.(tea.ResizeScrollbackMsg); ok && !m.initialized {
		m.initialized = true
		close(m.ready)
	}
	return m, cmd
}

func (m *rendererStartupModel) wait(t *testing.T) {
	t.Helper()
	select {
	case <-m.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("renderer did not finish its initial resize")
	}
}

func TestRendererStartupWaitsForResizeReport(t *testing.T) {
	m := fixedComposerModel(80, 24)
	startup := &rendererStartupModel{Model: m, ready: make(chan struct{})}
	startup.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.flush.resizedFrom == 0 {
		t.Fatal("fixture must be waiting for the resize report")
	}
	if cmd := m.queueScrollbackPrint("OLD\n"+strings.Repeat("ROW\n", 100), 0); cmd != nil || len(m.flush.pendingPrints) != 0 {
		t.Fatal("overflow must wait for the resize report")
	}
	select {
	case <-startup.ready:
		t.Fatal("startup completed before the resize report")
	default:
	}

	startup.Update(tea.ResizeScrollbackMsg{})
	select {
	case <-startup.ready:
	default:
		t.Fatal("startup did not complete after the resize report")
	}
	if m.flush.resizedFrom != 0 || len(m.flush.pendingPrints) == 0 {
		t.Fatal("startup completed before the model released its overflow")
	}
	// Later resize reports must not close the readiness channel again.
	startup.Update(tea.ResizeScrollbackMsg{})
}
