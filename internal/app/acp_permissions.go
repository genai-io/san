package app

import (
	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/setting"
)

// acpPermissions owns session grants and mode; directory policies share that state.
type acpPermissions struct {
	*setting.SessionPermissions
	settings   *setting.Data
	autoMemory system.MemoryStores
}

func (p *acpPermissions) ForDirectory(cwd string) agent.PermissionPolicy {
	return agent.PermissionPolicy{
		Settings:   setting.ScopedPermissions{Data: p.settings, CWD: cwd},
		Session:    p.SessionPermissions,
		AutoMemory: p.autoMemory,
	}
}
