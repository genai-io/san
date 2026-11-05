package subagent

import (
	"context"

	"github.com/genai-io/san/internal/tool/perm"
)

// Owned workers never let hook approval bypass either child's constraints or
// the owner's rules. A forced human decision without an explicit receiver is
// denied rather than being silently treated as an ordinary permit.
type ownedPermission struct{ check perm.PermissionFunc }

func (p ownedPermission) CanApproveByHook(string, map[string]any) bool { return false }
func (p ownedPermission) Check(ctx context.Context, name string, input map[string]any, forced bool, reason string) (bool, string) {
	if forced {
		return false, "worker hook requires explicit approval: " + reason
	}
	return p.check(ctx, name, input)
}
