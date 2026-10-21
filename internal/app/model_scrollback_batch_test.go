package app

import (
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func scrollbackBatchModel(t *testing.T, rows string) (*model, *synchronizedOutput, uint64) {
	t.Helper()
	m := fixedComposerModel(80, 24)
	output := &synchronizedOutput{writes: make(chan struct{}, 10)}
	m.flush.output = &terminalOutput{writer: output}
	m.flush.queueScrollbackPrint(rows, 0)
	m.Update(tea.KeyReleaseMsg{})
	if len(m.flush.pendingPrints) != 1 || m.flush.frameForPrint != nil {
		t.Fatal("fixture must queue a history handoff without starting it")
	}
	return m, output, m.flush.pendingPrints[0].id
}

func writeTerminal(t *testing.T, m *model, text string) {
	t.Helper()
	if _, err := m.flush.output.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
}

// Every frame of a scroll handoff is whole on its own, so none is held back.
func TestScrollbackScrollHandoffWritesThrough(t *testing.T) {
	m, output, id := scrollbackBatchModel(t, strings.Repeat("ROW\n", 30)+"LAST")
	writeTerminal(t, m, "queued|")
	m.Update(scrollbackPrintReadyMsg{id: id})
	if !m.flush.pendingPrints[0].scrolling || len(m.flush.pendingPrints[0].rows) == 0 {
		t.Fatal("fixture must scroll the first of several chunks")
	}
	writeTerminal(t, m, "first|")
	for len(m.flush.pendingPrints) > 0 {
		m.Update(tea.ScrollUpMsg{Scrolled: true})
		writeTerminal(t, m, "next|")
	}
	if got := output.String(); !strings.HasPrefix(got, "queued|first|next|") {
		t.Fatalf("scroll handoff held output back: %q", got)
	}
}

// An insert shifts the frame between the renderer's flush and the Println, so
// both reach the terminal together.
func TestScrollbackInsertHoldsOutputUntilItLands(t *testing.T) {
	m, output, id := scrollbackBatchModel(t, "ROW\nLAST")
	m.Update(scrollbackPrintReadyMsg{id: id})
	m.Update(tea.ScrollUpMsg{Scrolled: false})
	if m.flush.pendingPrints[0].scrolling || m.flush.frameForPrint == nil {
		t.Fatal("a refused scroll must insert the chunk above one row")
	}
	writeTerminal(t, m, "frame|insert|")
	if output.String() != "" {
		t.Fatal("the insert reached the terminal before it landed")
	}
	m.Update(scrollbackPrintDoneMsg{id: id})
	writeTerminal(t, m, "restored")
	if got := output.String(); got != "frame|insert|restored" || len(m.flush.pendingPrints) != 0 {
		t.Fatalf("insert did not land and release output: %q", got)
	}
}

func TestScrollbackOutputReportsWriteFailure(t *testing.T) {
	m, _, _ := scrollbackBatchModel(t, "SETTLED")
	m.flush.output.writer = terminalWriterFunc(func([]byte) (int, error) { return 0, io.ErrClosedPipe })
	_, _ = m.flush.output.Write([]byte("frame"))
	m.clearScrollback(nil)
	_, cmd := m.Update(tea.KeyReleaseMsg{})
	if cmd == nil {
		t.Fatal("a failed terminal write did not stop the program")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok || !errors.Is(m.flush.output.err, io.ErrClosedPipe) {
		t.Fatal("terminal output failure was lost")
	}
}
