package plugin

import "context"

type registryKey struct{}

// WithRegistry prevents subprocess environments from using another session's plugins.
func WithRegistry(ctx context.Context, registry *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, registry)
}

func registryFromContext(ctx context.Context) *Registry {
	if ctx != nil {
		if registry, ok := ctx.Value(registryKey{}).(*Registry); ok {
			return registry
		}
	}
	return defaultRegistry
}
