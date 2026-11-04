//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/acp"
	"golang.org/x/sys/unix"
)

func TestACPStdioRejectsRegularFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if stream, err := acpStdio(file); err == nil {
		_ = stream.Close()
		t.Fatal("regular file accepted as interruptible stdio")
	}
}

// Model inherited blocking stdin/stdout, not io.Pipe or os.Pipe's usual
// poller-managed wrappers. Leave stdin open and never drain stdout: shutdown
// must interrupt both the active read and the full-pipe frame write.
func TestACPStdioWriteTimeout(t *testing.T) {
	stdin, peerInput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer peerInput.Close()
	peerOutput, stdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer peerOutput.Close()
	defer stdout.Close()
	for _, file := range []*os.File{stdin, stdout} {
		if err := unix.SetNonblock(int(file.Fd()), false); err != nil {
			t.Fatal(err)
		}
	}
	input, err := acpStdio(stdin)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := acpStdio(stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := acp.New(ctx, input, output, acp.Handlers{}, acp.Limits{WriteTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Notify(ctx, "test", map[string]string{"text": strings.Repeat("x", 512<<10)}); !errors.Is(err, acp.ErrWriteTimeout) {
		t.Fatalf("blocked frame = %v", err)
	}
	if err := conn.Wait(ctx); !errors.Is(err, acp.ErrWriteTimeout) {
		t.Fatalf("read/write join = %v", err)
	}
}
