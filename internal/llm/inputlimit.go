package llm

import (
	"os"
	"strconv"
)

// The context window is the denominator of every "how full is the context"
// question San asks — the status bar's percentage and the agent's
// auto-compaction trigger. Both must get the same answer, so both resolve it
// here. Issue #338 was the display and the trigger disagreeing; keeping one
// resolver is what stops that from recurring.

const (
	// DefaultInputLimit is the window assumed when nothing else supplies one.
	// Auto-compaction only runs against a non-zero limit, so returning 0 for an
	// undiscoverable model would disable it entirely and let the conversation
	// grow until the provider rejected it. 128k is the smallest window in
	// common use, so this under-guesses on roomier models — compacting earlier
	// than strictly necessary — rather than over-guessing and overflowing.
	DefaultInputLimit = 128_000

	// InputLimitEnvVar forces the window for models San cannot size, e.g. an
	// aggregator serving a 1M-token model without publishing limits.
	InputLimitEnvVar = "SAN_INPUT_LIMIT"
)

// inputLimitOverride returns the window forced by InputLimitEnvVar, or 0 when
// unset or not a positive integer.
func inputLimitOverride() int {
	n, err := strconv.Atoi(os.Getenv(InputLimitEnvVar))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// EffectiveInputLimit resolves a model's context window from configuration and
// cache, returning 0 when it cannot be determined — callers decide whether an
// unknown window means DefaultInputLimit or "no model, show nothing".
//
// Order: the env override, then the user's configured limit, then this
// provider+auth's cached figure, then the largest figure cached for the ID
// under any provider (an aggregator may serve a model without publishing its
// window while the native provider knows it).
//
// auth disambiguates a model ID cached under several auth methods with
// different windows (gpt-5.5: 400k via the API, 272k via a subscription).
func (s *Store) EffectiveInputLimit(provider Name, auth AuthMethod, modelID string) int {
	if n := inputLimitOverride(); n > 0 {
		return n
	}
	if s == nil || modelID == "" {
		return 0
	}
	if in, _, ok := s.GetTokenLimit(modelID); ok && in > 0 {
		return in
	}
	if in, _ := s.CachedModelLimitsForProvider(provider, auth, modelID); in > 0 {
		return in
	}
	in, _ := s.CachedModelLimits(modelID)
	return in
}
