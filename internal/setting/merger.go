package setting

import "maps"

// mergeSettings merges two Data, with overlay taking precedence over base.
// Slices (permission rules) are deduplicated unions. Maps are merged with overlay winning on conflicts.
func mergeSettings(base, overlay *Data) *Data {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}

	result := NewData()
	result.Permissions = mergePermissions(base.Permissions, overlay.Permissions)
	result.Model = coalesce(overlay.Model, base.Model)
	result.Theme = coalesce(overlay.Theme, base.Theme)
	result.Hooks = mergeHooks(base.Hooks, overlay.Hooks)
	result.Env = mergeMaps(base.Env, overlay.Env)
	result.EnabledPlugins = mergeMaps(base.EnabledPlugins, overlay.EnabledPlugins)
	result.DisabledTools = mergeMaps(base.DisabledTools, overlay.DisabledTools)
	result.SearchProvider = coalesce(overlay.SearchProvider, base.SearchProvider)
	result.AllowBypass = coalesceBool(overlay.AllowBypass, base.AllowBypass)
	result.ContextBar = coalesceBool(overlay.ContextBar, base.ContextBar)
	result.Persona = coalesce(overlay.Persona, base.Persona)
	result.SelfLearn = mergeSelfLearn(base.SelfLearn, overlay.SelfLearn)
	result.AutoPilot = mergeAutoPilot(base.AutoPilot, overlay.AutoPilot)

	return result
}

// mergeAutoPilot does a field-level merge of the autopilot config: strings and
// the continuation cap coalesce (overlay's non-zero wins), steer bools OR
// (enable-anywhere wins), and the tri-state permission steer coalesces (overlay's
// explicit value wins, else base's, else nil = default on). Without this the
// entire autoPilot block is dropped on every Load and every save.
func mergeAutoPilot(base, overlay AutoPilotSettings) AutoPilotSettings {
	return AutoPilotSettings{
		Model:            coalesce(overlay.Model, base.Model),
		SystemPrompt:     coalesce(overlay.SystemPrompt, base.SystemPrompt),
		SystemPromptFile: coalesce(overlay.SystemPromptFile, base.SystemPromptFile),
		Mission:          coalesce(overlay.Mission, base.Mission),
		MaxContinuations: coalesceInt(overlay.MaxContinuations, base.MaxContinuations),
		Steers: SteerSettings{
			Suggest:    overlay.Steers.Suggest || base.Steers.Suggest,
			TurnStart:  overlay.Steers.TurnStart || base.Steers.TurnStart,
			Permission: coalesceBool(overlay.Steers.Permission, base.Steers.Permission),
			BashPrompt: overlay.Steers.BashPrompt || base.Steers.BashPrompt,
			Question:   overlay.Steers.Question || base.Steers.Question,
			TurnEnd:    overlay.Steers.TurnEnd || base.Steers.TurnEnd,
		},
	}
}

// ApplyPersonaOverlay merges a persona's settings.json overlay on top of the
// resolved base settings, as the highest file-level layer. Maps merge per key
// (a persona can even re-enable a tool a lower layer disabled, via
// "disabledTools": {"X": false}); permission allow/deny/ask are deduplicated
// unions, so a persona can tighten but never loosen them. A nil overlay
// returns base unchanged.
//
// The overlay's own Persona selector is ignored: a persona cannot re-select the
// active persona (which would be circular).
func ApplyPersonaOverlay(base, overlay *Data) *Data {
	if overlay == nil {
		return base
	}
	ov := overlay.Clone()
	ov.Persona = ""
	return mergeSettings(base, ov)
}

// mergeSelfLearn does a field-level merge of the L1 configuration: integers
// coalesce (non-zero wins), bools OR (deny-anywhere or enable-anywhere
// wins, matching the safety bias of layered config). Without this the
// entire selfLearn block is dropped on every Load and every save.
func mergeSelfLearn(base, overlay SelfLearnSettings) SelfLearnSettings {
	return SelfLearnSettings{
		Memory: SelfLearnMemory{
			Enabled:    overlay.Memory.Enabled || base.Memory.Enabled,
			EveryTurns: coalesceInt(overlay.Memory.EveryTurns, base.Memory.EveryTurns),
			MaxKB:      coalesceInt(overlay.Memory.MaxKB, base.Memory.MaxKB),
		},
		Skills: SelfLearnSkills{
			Enabled:                overlay.Skills.Enabled || base.Skills.Enabled,
			EveryToolIters:         coalesceInt(overlay.Skills.EveryToolIters, base.Skills.EveryToolIters),
			DenyCreate:             overlay.Skills.DenyCreate || base.Skills.DenyCreate,
			DenyUpdate:             overlay.Skills.DenyUpdate || base.Skills.DenyUpdate,
			DenyDelete:             overlay.Skills.DenyDelete || base.Skills.DenyDelete,
			AllowUpdateUserCreated: overlay.Skills.AllowUpdateUserCreated || base.Skills.AllowUpdateUserCreated,
		},
	}
}

func coalesceInt(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

func mergePermissions(base, overlay PermissionSettings) PermissionSettings {
	return PermissionSettings{
		DefaultMode: coalesce(overlay.DefaultMode, base.DefaultMode),
		Allow:       mergeStringSlices(base.Allow, overlay.Allow),
		Deny:        mergeStringSlices(base.Deny, overlay.Deny),
		Ask:         mergeStringSlices(base.Ask, overlay.Ask),
	}
}

func mergeStringSlices(base, overlay []string) []string {
	seen := make(map[string]bool, len(base)+len(overlay))
	result := make([]string, 0, len(base)+len(overlay))
	for _, s := range append(base, overlay...) {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// mergeHooks merges hook configurations, appending overlay hooks to base hooks per event.
func mergeHooks(base, overlay map[string][]Hook) map[string][]Hook {
	result := make(map[string][]Hook, len(base)+len(overlay))
	for k, v := range base {
		result[k] = append([]Hook{}, v...)
	}
	for k, v := range overlay {
		result[k] = append(result[k], v...)
	}
	return result
}

// coalesce returns the first non-empty string.
func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func coalesceBool(a, b *bool) *bool {
	if a != nil {
		return a
	}
	return b
}

// mergeMaps merges two maps with overlay taking precedence over base.
func mergeMaps[V any](base, overlay map[string]V) map[string]V {
	result := make(map[string]V, len(base)+len(overlay))
	maps.Copy(result, base)
	maps.Copy(result, overlay)
	return result
}
