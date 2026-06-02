package selflearn

import (
	"fmt"

	"github.com/genai-io/gen-code/internal/setting"
)

// Resolved is the runtime bundle for standing up L1 from configuration:
// trigger arms (Config), skill action permissions (Perms), and memory
// per-file char cap (MemoryMaxChars). The single bridge between the
// setting layer and this package — built once per session.
type Resolved struct {
	Config         Config
	Perms          ActionPermissions
	MemoryMaxChars int
}

// ResolveSettings validates and converts the raw settings into Resolved,
// applying §3.1 defaults for unset fields.
func ResolveSettings(s setting.SelfLearnSettings) (Resolved, error) {
	if err := s.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("self-learning config invalid: %w", err)
	}
	return Resolved{
		Config: Config{
			Memory: Arm{Enabled: s.Memory.Enabled, Interval: s.Memory.EveryTurns},
			Skills: Arm{Enabled: s.Skills.Enabled, Interval: s.Skills.EveryToolIters},
		},
		Perms: ActionPermissions{
			AllowCreate:            s.Skills.AllowCreate(),
			AllowUpdate:            s.Skills.AllowUpdate(),
			AllowDelete:            s.Skills.AllowDelete(),
			AllowUpdateUserCreated: s.Skills.AllowUpdateUserCreated,
		},
		MemoryMaxChars: s.Memory.MaxKBOr() * 1024,
	}, nil
}
