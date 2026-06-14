package llm

// CostEstimator computes the money cost of a single inference from its token
// usage, returning (_, false) when the model's pricing is unknown. Each
// provider package registers its own in init() via RegisterCostEstimator, so
// callers price a turn through EstimateCost without importing provider
// internals or switching on the provider name.
type CostEstimator func(modelID string, usage Usage) (Money, bool)

// RegisterCostEstimator registers a provider's cost estimator. Call once per
// provider package, typically alongside Register() in init().
func RegisterCostEstimator(provider Name, fn CostEstimator) {
	globalRegistry.RegisterCostEstimator(provider, fn)
}

// RegisterCostEstimator registers a provider's cost estimator.
func (r *Registry) RegisterCostEstimator(provider Name, fn CostEstimator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.costEstimators[provider] = fn
}

// EstimateCost dispatches to the estimator registered for provider, or returns
// (Money{}, false) when the provider has no registered pricing.
func EstimateCost(provider Name, modelID string, usage Usage) (Money, bool) {
	return globalRegistry.EstimateCost(provider, modelID, usage)
}

// EstimateCost dispatches to the estimator registered for provider, or returns
// (Money{}, false) when the provider has no registered pricing.
func (r *Registry) EstimateCost(provider Name, modelID string, usage Usage) (Money, bool) {
	r.mu.RLock()
	fn, ok := r.costEstimators[provider]
	r.mu.RUnlock()
	if !ok {
		return Money{}, false
	}
	return fn(modelID, usage)
}
