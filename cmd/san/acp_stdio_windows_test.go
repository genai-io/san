//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/acp"
	"golang.org/x/sys/windows"
)

// CreatePipe gives us the synchronous anonymous handles inherited by editor
// children, independently of whether Go changes os.Pipe to use overlapped I/O.
func acpTestPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	var r, w windows.Handle
	if err := windows.CreatePipe(&r, &w, nil, 4096); err != nil {
		t.Fatal(err)
	}
	reader, writer := os.NewFile(uintptr(r), "reader"), os.NewFile(uintptr(w), "writer")
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	return reader, writer
}

func acpTestStream(t *testing.T, file *os.File) *acpPipe {
	t.Helper()
	stream, err := acpStdio(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	pipe, ok := stream.(*acpPipe)
	if !ok {
		t.Fatal("CreatePipe did not select synchronous adapter")
	}
	return pipe
}

func TestACPStdioHealthyFrames(t *testing.T) {
	stdin, peerInput := acpTestPipe(t)
	peerOutput, stdout := acpTestPipe(t)
	input, output := acpTestStream(t, stdin), acpTestStream(t, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := acp.New(ctx, input, output, acp.Handlers{}, acp.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Small complete frames fit the pipe. Verify ordering and the JSON payload,
	// rather than merely observing that a write returned.
	for i := range 10 {
		if err := conn.Notify(ctx, "test", i); err != nil {
			t.Fatal(err)
		}
	}
	scanner := bufio.NewScanner(peerOutput)
	for i := range 10 {
		if !scanner.Scan() {
			t.Fatalf("frame %d: %v", i, scanner.Err())
		}
		var frame struct {
			Method string `json:"method"`
			Params int    `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil || frame.Method != "test" || frame.Params != i {
			t.Fatalf("frame %d = %s (%v)", i, scanner.Bytes(), err)
		}
	}
	_ = peerInput.Close()
	if err := conn.Wait(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF join = %v", err)
	}
}

func TestACPStdioWriteTimeout(t *testing.T) {
	stdin, peerInput := acpTestPipe(t)
	_, stdout := acpTestPipe(t)
	input, output := acpTestStream(t, stdin), acpTestStream(t, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, joined := make(chan struct{}), make(chan struct{})
	conn, err := acp.New(ctx, input, output, acp.Handlers{
		Notification: func(ctx context.Context, _ *acp.Conn, _ string, _ json.RawMessage) error {
			close(entered)
			<-ctx.Done()
			close(joined)
			return ctx.Err()
		},
	}, acp.Limits{WriteTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(peerInput, "{\"jsonrpc\":\"2.0\",\"method\":\"test\"}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("callback did not start")
	}
	if err := conn.Notify(ctx, "test", strings.Repeat("x", 512<<10)); !errors.Is(err, acp.ErrWriteTimeout) {
		t.Fatalf("partial/blocked frame acknowledged: %v", err)
	}
	if err := conn.Wait(ctx); !errors.Is(err, acp.ErrWriteTimeout) {
		t.Fatalf("I/O join = %v", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("Wait did not join callback")
	}
	if err := conn.Notify(ctx, "later", nil); err == nil {
		t.Fatal("timed-out connection accepted another frame")
	}
}

func TestACPStdioCloseInterrupts(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			for range 50 {
				r, w := acpTestPipe(t)
				file := r
				if write {
					file = w
				}
				stream := acpTestStream(t, file)
				done := make(chan error, 1)
				go func() {
					var err error
					if write {
						_, err = stream.Write(make([]byte, 512<<10))
					} else {
						_, err = stream.Read(make([]byte, 1))
					}
					done <- err
				}()
				// Wait for publication, not a sleep: Close can race the actual
				// syscall entry and must keep cancelling across that window.
				deadline := time.Now().Add(5 * time.Second)
				for {
					stream.mu.Lock()
					active := stream.thread != 0
					stream.mu.Unlock()
					if active {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("I/O did not start")
					}
					time.Sleep(time.Millisecond)
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if !errors.Is(err, os.ErrClosed) {
						t.Fatalf("interrupted operation = %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("I/O did not join")
				}
				if err := stream.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := stream.Read(nil); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("read after close = %v", err)
				}
				// Closing the adapter must not take ownership of inherited handles.
				if _, err := file.Stat(); err != nil {
					t.Fatalf("original handle closed: %v", err)
				}
				_ = r.Close()
				_ = w.Close()
			}
		})
	}
}

func TestACPStdioCloseBeforeSyscall(t *testing.T) {
	r, _ := acpTestPipe(t)
	stream := acpTestStream(t, r)
	published, enter, retired := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	// Exercise Close's non-sticky cancellation window deterministically: a
	// locked thread is published, but has not yet submitted its synchronous I/O.
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		stream.mu.Lock()
		process := windows.CurrentProcess()
		err := windows.DuplicateHandle(process, windows.CurrentThread(), process, &stream.thread, 0, false, windows.DUPLICATE_SAME_ACCESS)
		stream.mu.Unlock()
		close(published)
		<-enter
		if err == nil {
			var b [1]byte
			var n uint32
			err = windows.ReadFile(stream.handle, b[:], &n, nil)
			stream.mu.Lock()
			_ = windows.CloseHandle(stream.thread)
			stream.thread = 0
			stream.mu.Unlock()
		}
		retired <- err
	}()
	<-published
	closed := make(chan error, 1)
	go func() { closed <- stream.Close() }()
	for {
		stream.mu.Lock()
		closing := stream.closed
		stream.mu.Unlock()
		if closing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(enter)
	select {
	case err := <-retired:
		if err != windows.ERROR_OPERATION_ABORTED {
			t.Fatalf("late syscall = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close missed syscall entry")
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestACPStdioOverlappedPipe(t *testing.T) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\san-acp-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	r, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(r), "overlapped-reader")
	defer reader.Close()
	w, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		t.Fatal(err)
	}
	writer := os.NewFile(uintptr(w), "overlapped-writer")
	defer writer.Close()
	input, err := acpStdio(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := acpStdio(writer)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if _, ok := input.(*os.File); !ok {
		t.Fatal("overlapped pipe did not select Go poller")
	}
	if _, err := output.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	var b [5]byte
	if _, err := io.ReadFull(input, b[:]); err != nil || string(b[:]) != "hello" {
		t.Fatalf("read = %q, %v", b, err)
	}
	for _, write := range []bool{false, true} {
		done := make(chan error, 1)
		go func() {
			var err error
			if write {
				_, err = output.Write(make([]byte, 512<<10))
			} else {
				_, err = input.Read(b[:])
			}
			done <- err
		}()
		time.Sleep(20 * time.Millisecond)
		if write {
			_ = output.Close()
		} else {
			_ = input.Close()
		}
		select {
		case err := <-done:
			if !errors.Is(err, os.ErrClosed) {
				t.Fatalf("interrupted overlapped I/O = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("overlapped I/O did not join")
		}
	}
}

func TestACPStdioRejectsRegularFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-pipe")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for range 50 {
		if stream, err := acpStdio(file); err == nil {
			_ = stream.Close()
			t.Fatal("regular file accepted")
		}
	}
	if _, err := file.WriteString("still owned by caller"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := acpStdio(file); err == nil {
		t.Fatal("closed handle accepted")
	}
}
