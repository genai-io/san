package selflearn

import (
	"fmt"

	"github.com/genai-io/gen-code/internal/setting"
)

// Resolved is the runtime bundle a caller needs to stand up L1 from
// configuration: the trigger arms (Config), the skill action permissions
// (Perms), and the memory per-file char cap (MemoryMaxChars). It is the
// single bridge point between the setting layer and this package — the
// app's wire-up code lives upstream of here and shouldn't poke individual
// fields itself.
//
// Build it once from setting.SelfLearnSettings at session start and pass
// the pieces to NewMemoryStoreWithCap, NewSkillManager, and New.
type Resolved struct {
	Config         Config
	Perms          ActionPermissions
	MemoryMaxChars int
}

// ResolveSettings converts a setting.SelfLearnSettings into the runtime
// values L1 needs. It runs Validate first; any illegal combination
// (§3.1) is returned verbatim so the caller can surface it at startup.
// Defaults are applied for unset fields (everyTurns, everyToolIters,
// maxKB) per §3.1.
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
			AllowCreate:            s.Skills.AllowCreateOr(),
			AllowUpdate:            s.Skills.AllowUpdateOr(),
			AllowDelete:            s.Skills.AllowDeleteOr(),
			AllowUpdateUserCreated: s.Skills.AllowUpdateUserCreated,
		},
		MemoryMaxChars: s.Memory.MaxKBOr() * 1024,
	}, nil
}
