package todo

import "context"

type storeKey struct{}

// WithStore binds a session's tracker to tool contexts.
func WithStore(ctx context.Context, store *Store) context.Context {
	return context.WithValue(ctx, storeKey{}, store)
}

// FromContext returns the bound tracker, or nil when the host offers none.
func FromContext(ctx context.Context) *Store {
	store, _ := ctx.Value(storeKey{}).(*Store)
	return store
}
