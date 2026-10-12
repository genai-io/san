package app

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTerminalOutputPreservesFileDescriptor(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal-output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := &terminalOutput{writer: file}
	if writer.Fd() != file.Fd() {
		t.Fatal("wrapping terminal output changed its descriptor")
	}
}

func TestTerminalOutputWritesHandoffTogether(t *testing.T) {
	for _, synchronized := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "synchronized"}[synchronized], func(t *testing.T) {
			output := &synchronizedOutput{writes: make(chan struct{}, 10)}
			writer := &terminalOutput{writer: output}
			if _, err := writer.Write([]byte("before")); err != nil {
				t.Fatal(err)
			}
			writer.Begin()
			redraw, want := "redraw", "redrawhistory"
			if synchronized {
				redraw = ansi.SetModeSynchronizedOutput + redraw + ansi.ResetModeSynchronizedOutput
				want = ansi.SetModeSynchronizedOutput + want + ansi.ResetModeSynchronizedOutput
			}
			for _, part := range []string{redraw, "history"} {
				if _, err := writer.Write([]byte(part)); err != nil {
					t.Fatal(err)
				}
			}
			if output.String() != "before" {
				t.Fatal("shortened frame reached the terminal before history insertion")
			}
			if err := writer.Flush(); err != nil {
				t.Fatal(err)
			}
			if output.String() != "before"+want || len(output.writes) != 2 {
				t.Fatalf("handoff used separate writes or ended synchronized output early: %q", output.String())
			}
			if _, err := writer.Write([]byte("after")); err != nil {
				t.Fatal(err)
			}
			if output.String() != "before"+want+"after" {
				t.Fatal("normal output stayed buffered after the handoff")
			}
		})
	}
}

type terminalWriterFunc func([]byte) (int, error)

func (f terminalWriterFunc) Write(p []byte) (int, error) { return f(p) }

func TestTerminalOutputKeepsWriteErrors(t *testing.T) {
	for _, want := range []error{io.ErrClosedPipe, io.ErrShortWrite} {
		t.Run(want.Error(), func(t *testing.T) {
			writer := &terminalOutput{writer: terminalWriterFunc(func([]byte) (int, error) {
				if want == io.ErrShortWrite {
					return 1, nil
				}
				return 0, want
			})}
			writer.Begin()
			if _, err := writer.Write([]byte("history")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Flush(); !errors.Is(err, want) {
				t.Fatalf("flush error = %v, want %v", err, want)
			}
			if err := writer.Flush(); !errors.Is(err, want) {
				t.Fatalf("shutdown lost the output error: %v", err)
			}
		})
	}
}
