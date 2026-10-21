package app

import (
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/core"
)

func queuedScrollbackBatch(t *testing.T, rows string) (*model, *synchronizedOutput, uint64) {
	t.Helper()
	m := fixedComposerModel(80, 24)
	m.queueScrollbackPrint(rows, 0)
	m.conv.Stream.Active = true
	m.conv.Messages = []core.ChatMessage{{Role: core.ChatAssistant, Content: "```text\n" + strings.Repeat("live output\n", 30)}}
	output := &synchronizedOutput{writes: make(chan struct{}, 10)}
	m.flush.output = &terminalOutput{writer: output}
	m.Update(tea.KeyReleaseMsg{})
	if len(m.flush.pendingPrints) != 1 || m.flush.frameForPrint != nil {
		t.Fatal("fixture must queue a history handoff without preparing it")
	}
	return m, output, m.flush.pendingPrints[0].id
}

func TestScrollbackBatchHoldsPreReadyAndBetweenChunkRedraws(t *testing.T) {
	m := fixedComposerModel(80, 24)
	output := &synchronizedOutput{writes: make(chan struct{}, 10)}
	m.flush.output = &terminalOutput{writer: output}
	m.flush.queueScrollbackPrint(strings.Repeat("ROW\n", 30)+"LAST", 0)
	m.Update(tea.KeyReleaseMsg{})
	id := m.flush.pendingPrints[0].id
	write := func(text string) {
		t.Helper()
		if _, err := m.flush.output.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	write("pre-ready")
	if output.String() != "" {
		t.Fatal("pre-ready redraw reached the terminal before the handoff")
	}
	m.Update(scrollbackPrintReadyMsg{id: id})
	if m.flush.frameForPrint == nil || len(m.flush.pendingPrints[0].rows) == 0 {
		t.Fatal("fixture must prepare only the first of two chunks")
	}
	write("first-scroll")
	m.Update(tea.ScrollUpMsg{Scrolled: true})
	write("second-scroll")
	if output.String() != "" {
		t.Fatal("between-chunk redraw escaped the batch")
	}
	for len(m.flush.pendingPrints) > 0 {
		m.Update(tea.ScrollUpMsg{Scrolled: true})
	}
	write("restored")
	if len(m.flush.pendingPrints) != 0 || output.String() != "pre-readyfirst-scrollsecond-scrollrestored" {
		t.Fatalf("history did not drain and release output: %q", output.String())
	}
}

func TestScrollbackBatchReleasesWhenCanceledOrDeferred(t *testing.T) {
	for _, action := range []string{"clear", "overlay"} {
		t.Run(action, func(t *testing.T) {
			m, output, id := queuedScrollbackBatch(t, "SETTLED")
			if _, err := m.flush.output.Write([]byte("pre-ready")); err != nil {
				t.Fatal(err)
			}
			if action == "clear" {
				m.clearScrollback(nil)
				m.Update(tea.KeyReleaseMsg{})
				m.Update(scrollbackPrintReadyMsg{id: id}) // stale delivery must not restart batching
			} else {
				m.userInput.Settings.Enter(m.env.Width, m.env.Height)
				m.Update(scrollbackPrintReadyMsg{id: id})
				if m.flush.frameForPrint != nil || len(m.flush.pendingPrints) != 1 {
					t.Fatal("overlay did not defer the queued print")
				}
			}
			if _, err := m.flush.output.Write([]byte("interactive-frame")); err != nil {
				t.Fatal(err)
			}
			if output.String() != "pre-readyinteractive-frame" {
				t.Fatalf("cancellation or overlay left output buffered: %q", output.String())
			}
			if action == "overlay" {
				_, resume := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				if resume == nil {
					t.Fatal("closing the overlay did not restart the handoff")
				}
				if _, err := m.flush.output.Write([]byte("resumed-frame")); err != nil {
					t.Fatal(err)
				}
				if output.String() != "pre-readyinteractive-frame" {
					t.Fatal("resumed pre-ready redraw escaped batching")
				}
				m.Update(scrollbackPrintReadyMsg{id: id})
				m.Update(scrollbackPrintDoneMsg{id: id})
				if output.String() != "pre-readyinteractive-frameresumed-frame" {
					t.Fatalf("resumed handoff did not release output: %q", output.String())
				}
			}
		})
	}
}

func TestScrollbackBatchReleaseReportsWriteFailure(t *testing.T) {
	m, _, _ := queuedScrollbackBatch(t, "SETTLED")
	m.flush.output.writer = terminalWriterFunc(func([]byte) (int, error) { return 0, io.ErrClosedPipe })
	if _, err := m.flush.output.Write([]byte("pre-ready")); err != nil {
		t.Fatal(err)
	}
	m.clearScrollback(nil)
	_, cmd := m.Update(tea.KeyReleaseMsg{})
	if cmd == nil {
		t.Fatal("releasing failed terminal output did not stop the program")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || !errors.Is(m.flush.output.err, io.ErrClosedPipe) {
		t.Fatal("terminal output failure was lost")
	}
}
