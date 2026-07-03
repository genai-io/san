package llm

import (
	"context"
	"fmt"
)

// Authenticator performs interactive (non-API-key) sign-in for a provider auth
// method — e.g. an OAuth subscription login. Provider packages register one per
// (provider, authMethod) alongside their factory, so product code can trigger
// sign-in/sign-out through this facade without importing the concrete provider
// package.
type Authenticator interface {
	// Login runs the interactive sign-in, persisting credentials on success.
	// onURL, if non-nil, receives a URL the user must visit — useful when a
	// browser cannot be opened automatically (e.g. over SSH).
	Login(ctx context.Context, onURL func(string)) error
	// Logout clears any stored credentials for the auth method.
	Logout() error
}

// StoredCredentialAuthenticator is an optional extension for authenticators that
// can report whether they already have local credentials worth validating.
type StoredCredentialAuthenticator interface {
	HasCredentials() bool
}

// RegisterAuthenticator registers the interactive login handler for a provider
// auth method.
func RegisterAuthenticator(provider Name, authMethod AuthMethod, a Authenticator) {
	globalRegistry.RegisterAuthenticator(provider, authMethod, a)
}

// RegisterAuthenticator registers the interactive login handler for a provider
// auth method.
func (r *Registry) RegisterAuthenticator(provider Name, authMethod AuthMethod, a Authenticator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authenticators[makeProviderKey(provider, authMethod)] = a
}

// SupportsInteractiveLogin reports whether a provider auth method signs in
// interactively (OAuth) rather than via an API key.
func SupportsInteractiveLogin(provider Name, authMethod AuthMethod) bool {
	return globalRegistry.authenticator(provider, authMethod) != nil
}

// HasInteractiveCredentials reports whether an interactive auth method already
// has stored credentials. Callers should still verify them with the provider,
// because this only checks local presence, not remote validity.
func HasInteractiveCredentials(provider Name, authMethod AuthMethod) bool {
	a := globalRegistry.authenticator(provider, authMethod)
	if a == nil {
		return false
	}
	withCredentials, ok := a.(StoredCredentialAuthenticator)
	return ok && withCredentials.HasCredentials()
}

// Login runs the interactive sign-in for a provider auth method.
func Login(ctx context.Context, provider Name, authMethod AuthMethod, onURL func(string)) error {
	a := globalRegistry.authenticator(provider, authMethod)
	if a == nil {
		return fmt.Errorf("provider %s:%s does not support interactive login", provider, authMethod)
	}
	return a.Login(ctx, onURL)
}

// Logout clears stored credentials for a provider auth method. It is a no-op for
// methods without an interactive authenticator (API-key credentials are cleared
// via the secret store instead).
func Logout(provider Name, authMethod AuthMethod) error {
	a := globalRegistry.authenticator(provider, authMethod)
	if a == nil {
		return nil
	}
	return a.Logout()
}

// authenticator returns the registered Authenticator for a provider auth method,
// or nil when none is registered.
func (r *Registry) authenticator(provider Name, authMethod AuthMethod) Authenticator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.authenticators[makeProviderKey(provider, authMethod)]
}
