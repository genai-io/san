package task

import "context"

type sessionKey struct{}
type ownerAgentKey struct{}

// WithOwnerAgentID identifies the agent launching background work; empty means main.
func WithOwnerAgentID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ownerAgentKey{}, id)
}

func OwnerAgentIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ownerAgentKey{}).(string)
	return id
}

// WithSessionID binds nested work and messages to the conversation that launched it.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionKey{}, id)
}

func SessionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}

// SetSessionID selects the owner for work launched outside an agent, such as /workflow.
func (m *Manager) SetSessionID(id string) {
	m.mu.Lock()
	m.sessionID = id
	m.mu.Unlock()
}

func (m *Manager) SessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessionID
}

func (m *Manager) ownerSession(ctx context.Context) string {
	if id, ok := ctx.Value(sessionKey{}).(string); ok {
		return id
	}
	return m.SessionID()
}
