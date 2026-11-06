package skill

import "context"

type registryKey struct{}

// WithRegistry binds skill discovery and execution to a session.
func WithRegistry(ctx context.Context, registry *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, registry)
}

// FromContext uses the native default only when the host has not supplied one.
func FromContext(ctx context.Context) *Registry {
	if registry, ok := ctx.Value(registryKey{}).(*Registry); ok {
		return registry
	}
	return Default()
}
