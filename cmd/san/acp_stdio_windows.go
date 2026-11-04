//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

// acpStdio owns a duplicate, never the inherited handle. Editor stdio must be
// pipes, not consoles or files. Overlapped pipes use Go's poller; inherited
// synchronous pipes need thread-directed cancellation instead.
func acpStdio(original *os.File) (io.ReadWriteCloser, error) {
	process := windows.CurrentProcess()
	var handle windows.Handle
	if err := windows.DuplicateHandle(process, windows.Handle(original.Fd()), process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, fmt.Errorf("ACP duplicate %s: %w", original.Name(), err)
	}
	fail := func(err error) (io.ReadWriteCloser, error) {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("ACP stdio %s: %w", original.Name(), err)
	}
	kind, err := windows.GetFileType(handle)
	if err != nil {
		return fail(err)
	}
	if kind != windows.FILE_TYPE_PIPE {
		return fail(fmt.Errorf("requires a pipe, not a console or regular file"))
	}
	// FILE_MODE_INFORMATION, as used by Go's Windows nonblocking detection.
	var mode uint32
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&mode)), uint32(unsafe.Sizeof(mode)), 16); err != nil {
		return fail(err)
	}
	if mode&(windows.FILE_SYNCHRONOUS_IO_ALERT|windows.FILE_SYNCHRONOUS_IO_NONALERT) == 0 {
		file := os.NewFile(uintptr(handle), original.Name())
		if err := file.SetDeadline(time.Time{}); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("ACP requires pollable overlapped pipes: %w", err)
		}
		return file, nil
	}
	if err := cancelSynchronousIO.Find(); err != nil {
		return fail(err)
	}
	return &acpPipe{handle: handle}, nil
}

// Each synchronous syscall runs on its caller's locked OS thread. No detached
// pump owns buffers or outlives Close. op serializes operations on this one-way
// stdio stream; mu protects the thread handle until cancellation has stopped.
type acpPipe struct {
	handle windows.Handle
	op     sync.Mutex
	mu     sync.Mutex
	thread windows.Handle
	closed bool
	once   sync.Once
	err    error
}

func (p *acpPipe) Read(b []byte) (int, error)  { return p.run(b, false) }
func (p *acpPipe) Write(b []byte) (int, error) { return p.run(b, true) }

func (p *acpPipe) run(b []byte, write bool) (int, error) {
	p.op.Lock()
	defer p.op.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, os.ErrClosed
	}
	process := windows.CurrentProcess()
	err := windows.DuplicateHandle(process, windows.CurrentThread(), process, &p.thread, 0, false, windows.DUPLICATE_SAME_ACCESS)
	p.mu.Unlock()
	if err != nil {
		return 0, err
	}
	defer func() {
		p.mu.Lock()
		_ = windows.CloseHandle(p.thread)
		p.thread = 0
		p.mu.Unlock()
	}()
	if len(b) == 0 {
		return 0, nil
	}
	if len(b) > 1<<30 {
		b = b[:1<<30]
	}
	var n uint32
	if write {
		err = windows.WriteFile(p.handle, b, &n, nil)
		if err == nil && int(n) != len(b) {
			err = io.ErrShortWrite
		}
	} else {
		err = windows.ReadFile(p.handle, b, &n, nil)
		if err == windows.ERROR_BROKEN_PIPE || (err == nil && n == 0) {
			err = io.EOF
		}
	}
	if err == windows.ERROR_OPERATION_ABORTED {
		err = os.ErrClosed
	}
	return int(n), err
}

func (p *acpPipe) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		for p.thread != 0 {
			// Cancellation is not sticky: Close may arrive between publishing
			// the thread and entering ReadFile/WriteFile. Retry until the caller
			// retires its thread, including after ERROR_NOT_FOUND. Holding mu
			// during cancellation prevents targeting a reused thread handle.
			_, _, _ = cancelSynchronousIO.Call(uintptr(p.thread))
			p.mu.Unlock()
			time.Sleep(time.Millisecond)
			p.mu.Lock()
		}
		p.err = windows.CloseHandle(p.handle)
		p.mu.Unlock()
	})
	return p.err
}
