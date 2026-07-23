package app

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	rendererBarrierOldFrame = "STALE-LIVE-FRAME\nSECOND-STALE-ROW"
	rendererBarrierNewFrame = "CURRENT-MANAGED-FRAME"
	rendererBarrierCommit   = "COMMITTED-SCROLLBACK-BLOCK"
)

type synchronizedOutput struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
}

func (o *synchronizedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buf.Write(p)
	o.mu.Unlock()
	select {
	case o.writes <- struct{}{}:
	default:
	}
	return n, err
}

func (o *synchronizedOutput) Reset() {
	o.mu.Lock()
	o.buf.Reset()
	o.mu.Unlock()
}

func (o *synchronizedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *synchronizedOutput) WaitFor(marker string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if strings.Contains(o.String(), marker) {
			return true
		}
		select {
		case <-o.writes:
		case <-timer.C:
			return false
		}
	}
}

type beginRendererBarrierHandoffMsg struct{}

type rendererBarrierModel struct {
	output    *synchronizedOutput
	committed bool
}

func (m *rendererBarrierModel) Init() tea.Cmd {
	return nil
}

func (m *rendererBarrierModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(beginRendererBarrierHandoffMsg); !ok {
		return m, nil
	}

	// Discard startup rendering. From here on the assertion only observes the
	// handoff: CURRENT must be flushed before COMMITTED is inserted.
	m.output.Reset()
	m.committed = true
	return m, tea.Sequence(
		tea.Println(rendererBarrierCommit),
		tea.Quit,
	)
}

func (m *rendererBarrierModel) View() tea.View {
	if m.committed {
		return tea.NewView(rendererBarrierNewFrame)
	}
	return tea.NewView(rendererBarrierOldFrame)
}

// A Println must flush the newest queued View before insertAbove scrolls the
// managed frame. Without the downstream Bubble Tea barrier, COMMITTED appears
// first and CURRENT is only flushed during shutdown, reproducing the stale-frame
// race behind #314 without depending on a real terminal.
func TestScrollbackPrintFlushesCurrentFrameBeforeInsert(t *testing.T) {
	output := &synchronizedOutput{writes: make(chan struct{}, 1)}
	m := &rendererBarrierModel{output: output}
	program := tea.NewProgram(
		m,
		tea.WithInput(nil),
		tea.WithOutput(output),
		// Waiting until the 1 FPS renderer writes the old frame gives the test
		// almost a full second to send Println before another ticker flush.
		tea.WithFPS(1),
		tea.WithWindowSize(80, 24),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	if !output.WaitFor(rendererBarrierOldFrame, 3*time.Second) {
		program.Kill()
		<-done
		t.Fatal("timed out waiting for the stale frame to reach the renderer")
	}

	// The old frame is now physical and the next automatic flush is one second
	// away. Ask Update to queue the shorter current frame and Println at once.
	output.Reset()
	program.Send(beginRendererBarrierHandoffMsg{})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run renderer handoff model: %v", err)
		}
	case <-time.After(3 * time.Second):
		program.Kill()
		<-done
		t.Fatal("timed out waiting for the renderer handoff")
	}

	rendered := output.String()
	currentAt := strings.Index(rendered, rendererBarrierNewFrame)
	committedAt := strings.Index(rendered, rendererBarrierCommit)
	if currentAt < 0 || committedAt < 0 {
		t.Fatalf(
			"handoff output missing markers (current=%d, committed=%d): %q",
			currentAt,
			committedAt,
			rendered,
		)
	}
	if currentAt > committedAt {
		t.Fatalf(
			"scrollback inserted before the current managed frame was flushed "+
				"(current=%d, committed=%d): %q",
			currentAt,
			committedAt,
			rendered,
		)
	}
}
