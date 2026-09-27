//go:build !windows

package fs

// decodeOutput returns a command's output as is: outside Windows, programs
// write UTF-8.
func decodeOutput(b []byte) string { return string(b) }
