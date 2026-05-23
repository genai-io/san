// Package proc provides cross-platform helpers for managing spawned child
// processes as a group. On Unix the spawned process is placed in its own
// process group so the whole group (including descendants) can be terminated
// together; on Windows, which has no equivalent group concept exposed through
// the syscall package, the helpers fall back to terminating just the direct
// child via Process.Kill.
package proc
