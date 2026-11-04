//go:build unix

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Inherited stdio may be blocking and outside Go's poller: Close alone cannot
// interrupt its I/O. Duplicate ownership, enable nonblocking mode before NewFile,
// and require poller support. Only ACP uses these streams; O_NONBLOCK also affects
// the original descriptors, which must not be used concurrently by another owner.
func acpStdio(original *os.File) (*os.File, error) {
	info, err := original.Stat()
	if err != nil {
		return nil, fmt.Errorf("ACP stat %s: %w", original.Name(), err)
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return nil, fmt.Errorf("ACP requires stdio pipes or sockets, not %s", original.Name())
	}
	fd, err := unix.FcntlInt(original.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("ACP duplicate %s: %w", original.Name(), err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("ACP nonblocking %s: %w", original.Name(), err)
	}
	stream := os.NewFile(uintptr(fd), original.Name())
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("ACP requires pollable stdio pipes: %w", err)
	}
	return stream, nil
}
