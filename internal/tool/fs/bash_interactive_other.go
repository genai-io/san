//go:build !unix

package fs

import (
	"context"
	"os/exec"
)

// runWithResponder has no pseudo-terminal off unix, so it declines
// (handled=false) and bash falls back to its normal execution path.
func runWithResponder(_ context.Context, _ string, _ *exec.Cmd, _ BashPromptResponder) (string, bool, error) {
	return "", false, nil
}
