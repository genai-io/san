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

// InputLimitEnvVar sets the window for a model San cannot size on its own,
// e.g. an aggregator serving a model without publishing its limits. There is
// deliberately no default to stand in for it: a guessed window is acted on
// silently, and guessing low costs real context on every compaction while
// guessing high never fires at all. An unknown window resolves to 0, which
// skips proactive compaction and leaves the prompt-too-long retry
// (isPromptTooLong) to recover — one wasted request, no invented number, and
// the status bar honestly reads "--" instead of a percentage of a guess.
const InputLimitEnvVar = "SAN_INPUT_LIMIT"

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
// cache, returning 0 when it cannot be determined. Callers treat 0 as
// "unknown" and skip whatever they would have done with a window rather than
// substituting a guess.
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
