//go:build !unix

package fs

import (
	"context"
	"fmt"
	"os/exec"
)

// runInteractive is unsupported off unix (no pseudo-terminal). The interactive
// path is never selected there, so this only keeps the package building.
func runInteractive(_ context.Context, _ string, _ *exec.Cmd, _ BashPromptResponder) (string, error) {
	return "", fmt.Errorf("interactive command execution is not supported on this platform")
}
