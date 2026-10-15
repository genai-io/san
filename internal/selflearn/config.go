package selflearn

import (
	"fmt"

	"github.com/genai-io/san/internal/setting"
)

// Config is the resolved L1 configuration the app passes into
// NewSkillManager and New. Built once per session via ResolveSettings —
// the single bridge between the setting layer and this package.
type Config struct {
	// Skills bounds what a model-triggered review may do to the skill set.
	Skills SkillPermissions

	// Strategy is the user's learning-strategy override; non-empty replaces
	// the built-in guidance in the reviewer prompt.
	Strategy string
}

// Enabled reports whether any skill action is allowed. When false the caller should not
// even construct a Reviewer (zero overhead).
func (c Config) Enabled() bool { return c.Skills.Any() }

// ResolveSettings validates the raw settings and returns the resolved
// Config, applying §3.1 defaults for unset fields.
func ResolveSettings(s setting.SelfLearnSettings) (Config, error) {
	if err := s.Validate(); err != nil {
		return Config{}, fmt.Errorf("self-learning config invalid: %w", err)
	}
	return Config{
		Skills: SkillPermissions{
			AllowCreate: s.Skills.AllowCreate(),
			AllowUpdate: s.Skills.AllowUpdate(),
			AllowDelete: s.Skills.AllowDelete(),
		},
		Strategy: s.Strategy,
	}, nil
}
