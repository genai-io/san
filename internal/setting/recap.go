package setting

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	RecapDefaultAfterMinutes = 5
	RecapDefaultTurns        = 3
	recapMaxAfterMinutes     = (1<<63 - 1) / int64(time.Minute)
)

// RecapSettings controls automatic recaps. Zero values inherit. A negative
// AfterMinutes disables automatic recaps without affecting /recap.
type RecapSettings struct {
	AfterMinutes int `json:"afterMinutes,omitempty"`
	Turns        int `json:"turns,omitempty"`
}

// AwayTime returns the resolved absence threshold, or zero when disabled.
func (r RecapSettings) AwayTime() time.Duration {
	return time.Duration(max(0, resolvedRecapMinutes(r.AfterMinutes))) * time.Minute
}

func resolvedRecapMinutes(minutes int) int {
	switch {
	case minutes < 0:
		return -1
	case minutes == 0 || int64(minutes) > recapMaxAfterMinutes:
		return RecapDefaultAfterMinutes
	default:
		return minutes
	}
}

// Recap returns live settings with defaults filled in. The old AutoPilot
// setting is a fallback when recap.afterMinutes has not been set.
func (s *Settings) Recap() RecapSettings {
	var cfg RecapSettings
	if s != nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.data != nil {
			cfg = s.data.Recap
			cfg.AfterMinutes = coalesceInt(cfg.AfterMinutes, s.data.AutoPilot.RecapAfterMinutes)
		}
	}
	cfg.AfterMinutes = resolvedRecapMinutes(cfg.AfterMinutes)
	if cfg.Turns <= 0 {
		cfg.Turns = RecapDefaultTurns
	}
	return cfg
}

func mergeRecap(base, overlay RecapSettings) RecapSettings {
	return RecapSettings{
		AfterMinutes: coalesceInt(overlay.AfterMinutes, base.AfterMinutes),
		Turns:        coalesceInt(overlay.Turns, base.Turns),
	}
}

// SaveRecapSettings writes the nonzero fields in patch without replacing other settings.
func SaveRecapSettings(cwd string, scope Scope, patch RecapSettings) error {
	if scope != ScopeUser && scope != ScopeProject {
		return fmt.Errorf("invalid settings scope: %q", scope)
	}
	if patch.AfterMinutes < -1 || int64(patch.AfterMinutes) > recapMaxAfterMinutes {
		return fmt.Errorf("away time must be -1 (off) or 1–%d minutes", recapMaxAfterMinutes)
	}
	if patch.Turns < 0 {
		return fmt.Errorf("turn threshold must be positive")
	}
	dir := NewLoaderForCwd(cwd).dir(scope)
	if dir == "" {
		return os.ErrNotExist
	}
	return mutateSettingsPath(filepath.Join(dir, "settings.json"), func(d *Data) {
		d.Recap = mergeRecap(d.Recap, patch)
	})
}
