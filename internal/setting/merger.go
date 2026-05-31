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
	result.Identity = coalesce(overlay.Identity, base.Identity)
	result.SelfLearn = mergeSelfLearn(base.SelfLearn, overlay.SelfLearn)

	return result
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
