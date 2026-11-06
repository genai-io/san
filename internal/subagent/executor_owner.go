package subagent

import (
	"context"

	"github.com/genai-io/san/internal/tool/perm"
)

// Hooks cannot bypass parent or child policy; forced approval fails closed.
type workerPermission struct{ check perm.PermissionFunc }

func (p workerPermission) CanApproveByHook(string, map[string]any) bool { return false }
func (p workerPermission) Check(ctx context.Context, name string, input map[string]any, forced bool, reason string) (bool, string) {
	if forced {
		return false, "worker hook requires explicit approval: " + reason
	}
	return p.check(ctx, name, input)
}
