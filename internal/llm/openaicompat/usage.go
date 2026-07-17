package openaicompat

// SplitInputTokens separates an OpenAI-family "full prompt" token count into
// the fresh (uncached) and cached-read halves the rest of the app expects.
// The Anthropic convention the app assumes is: InputTokens holds only fresh
// tokens, and the cached prefix lives in CacheReadInputTokens. OpenAI's Chat
// Completions and Responses APIs instead report the whole prompt in a single
// figure (prompt_tokens / input_tokens) and expose the cached slice under
// *_tokens_details.cached_tokens; callers pass both and receive the split.
//
// fresh + cached always equals the (non-negative) full prompt, so
// InferResponse.TotalInputTokens stays exactly the API's reported input and
// the bottom-bar ctx readout is unchanged. Splitting the cached portion out of
// InputTokens keeps a turn's per-step sums from multi-counting the re-read
// cache and lets cost accounting bill the cached prefix at the cache-read rate
// rather than the full input rate.
//
// The split is defensive against malformed wire data: a cached count that is
// negative, or larger than the full prompt, is clamped so fresh never goes
// negative.
func SplitInputTokens(fullInput, cachedTokens int) (fresh, cached int) {
	fullInput = max(fullInput, 0)
	cached = min(max(cachedTokens, 0), fullInput)
	return fullInput - cached, cached
}
