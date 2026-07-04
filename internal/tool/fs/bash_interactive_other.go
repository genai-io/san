//go:build !unix

package fs

import (
	"context"
	"fmt"
	"os/exec"
)

// supportsBashPTY is false off unix (no pseudo-terminal), so bash uses its
// normal execution path and never selects the interactive branch.
const supportsBashPTY = false

// runInteractive is unsupported off unix; it exists only to keep the package
// building. The supportsBashPTY gate keeps it from ever being called.
func runInteractive(_ context.Context, _ string, _ *exec.Cmd, _ BashPromptResponder) (string, error) {
	return "", fmt.Errorf("interactive command execution is not supported on this platform")
}
