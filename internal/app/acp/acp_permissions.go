package acp

import (
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/setting"
)

// acpPermissions owns session grants and mode; directory policies share that state.
type acpPermissions struct {
	*setting.SessionPermissions
	settings *setting.Data
}

func (p *acpPermissions) ForDirectory(cwd string) runtime.PermissionPolicy {
	return runtime.PermissionPolicy{
		Settings: setting.ScopedPermissions{Data: p.settings, CWD: cwd},
		Session:  p.SessionPermissions,
	}
}
