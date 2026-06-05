package selflearn

import (
	"fmt"

	"github.com/genai-io/san/v2/internal/setting"
)

// Config is the resolved L1 configuration the app passes into NewMemoryStore,
// NewSkillManager, and New. Built once per session via ResolveSettings —
// the single bridge between the setting layer and this package.
type Config struct {
	Memory         Arm // memory arm: enable + every-N-turns cadence
	Skills         Arm // skills arm: enable + every-N-tool-iters cadence
	Perms          ActionPermissions
	MemoryMaxChars int
}

// Enabled reports whether any arm is on. When false the caller should not
// even construct a Reviewer (zero overhead).
func (c Config) Enabled() bool { return c.Memory.Enabled || c.Skills.Enabled }

// ResolveSettings validates the raw settings and returns the resolved
// Config, applying §3.1 defaults for unset fields.
func ResolveSettings(s setting.SelfLearnSettings) (Config, error) {
	if err := s.Validate(); err != nil {
		return Config{}, fmt.Errorf("self-learning config invalid: %w", err)
	}
	return Config{
		Memory: Arm{Enabled: s.Memory.Enabled, Interval: s.Memory.ResolvedEveryTurns()},
		Skills: Arm{Enabled: s.Skills.Enabled, Interval: s.Skills.ResolvedEveryToolIters()},
		Perms: ActionPermissions{
			AllowCreate:            s.Skills.AllowCreate(),
			AllowUpdate:            s.Skills.AllowUpdate(),
			AllowDelete:            s.Skills.AllowDelete(),
			AllowUpdateUserCreated: s.Skills.AllowUpdateUserCreated,
		},
		MemoryMaxChars: s.Memory.ResolvedMaxKB() * 1024,
	}, nil
}
