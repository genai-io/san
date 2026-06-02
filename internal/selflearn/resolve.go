package selflearn

import (
	"fmt"

	"github.com/genai-io/gen-code/internal/setting"
)

// Runtime is the resolved L1 bundle the app passes into NewMemoryStore,
// NewSkillManager, and New. Built once per session via ResolveSettings —
// the single bridge between the setting layer and this package.
type Runtime struct {
	Config         Config
	Perms          ActionPermissions
	MemoryMaxChars int
}

// ResolveSettings validates the raw settings and returns the Runtime
// bundle, applying §3.1 defaults for unset fields.
func ResolveSettings(s setting.SelfLearnSettings) (Runtime, error) {
	if err := s.Validate(); err != nil {
		return Runtime{}, fmt.Errorf("self-learning config invalid: %w", err)
	}
	return Runtime{
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
