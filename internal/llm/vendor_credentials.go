package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/auth"

	"github.com/genai-io/san/internal/secret"
)

// A vendor's credential, where San already keeps it.
//
// credentials is San's secret store, presented as the SDK's credential store.
//
// The two disagree on shape, and the translation is the point: San's blobs
// carry fields the SDK's Credential has no room for, such as the ChatGPT
// account id an older San wrote. Save reads the stored blob back and writes
// only what the SDK actually rotated.
type credentials struct{}

// storeKey is where a vendor's credential lives in San's secret store.
func storeKey(vendorID string) (string, bool) {
	switch vendorID {
	case copilotVendor:
		return "GITHUB_COPILOT_AUTH", true
	case codexVendor:
		return "OPENAI_CHATGPT_AUTH", true
	}
	return "", false
}

// copilotBlob is San's stored Copilot credential. The GitHub token is the
// durable half; the rest caches the last exchange.
type copilotBlob struct {
	GitHubToken  string    `json:"github_token"`
	CopilotToken string    `json:"copilot_token,omitempty"`
	APIEndpoint  string    `json:"api_endpoint,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// codexBlob is San's stored ChatGPT credential.
type codexBlob struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

func (credentials) Load(vendorID string) (auth.Credential, bool, error) {
	key, ok := storeKey(vendorID)
	if !ok {
		return auth.Credential{}, false, nil
	}
	raw, err := storedCredential(key)
	if err != nil {
		return auth.Credential{}, false, err
	}
	if raw == "" {
		return auth.Credential{}, false, nil
	}

	switch vendorID {
	case copilotVendor:
		var blob copilotBlob
		if err := json.Unmarshal([]byte(raw), &blob); err != nil || blob.GitHubToken == "" {
			return auth.Credential{}, false, nil
		}
		return auth.Credential{
			Vendor:  vendorID,
			Access:  blob.GitHubToken,
			BaseURL: blob.APIEndpoint,
		}, true, nil
	default:
		var blob codexBlob
		if err := json.Unmarshal([]byte(raw), &blob); err != nil || blob.AccessToken == "" {
			return auth.Credential{}, false, nil
		}
		return auth.Credential{
			Vendor:    vendorID,
			Access:    blob.AccessToken,
			Refresh:   blob.RefreshToken,
			ExpiresAt: blob.ExpiresAt,
		}, true, nil
	}
}

func storedCredential(key string) (string, error) {
	store := secret.Default()
	if store == nil {
		return "", errors.New("llm: secret store unavailable")
	}
	raw, err := store.GetFresh(key)
	if err != nil {
		return "", err
	}
	// Environment credentials can bootstrap a sign-in, but cannot replace a
	// newer refresh token already persisted by this or another process.
	if raw == "" {
		raw = os.Getenv(key)
	}
	return raw, nil
}

func (credentials) WithRefreshLock(ctx context.Context, _ string, fn func() error) error {
	store := secret.Default()
	if store == nil {
		return errors.New("llm: secret store unavailable")
	}
	return store.WithRefreshLock(ctx, fn)
}

func (c credentials) Save(credential auth.Credential) error {
	key, ok := storeKey(credential.Vendor)
	if !ok {
		return fmt.Errorf("llm: %q keeps no interactive credential", credential.Vendor)
	}
	store := secret.Default()
	if store == nil {
		return errors.New("llm: secret store unavailable")
	}

	var raw []byte
	var err error
	switch credential.Vendor {
	case copilotVendor:
		raw, err = json.Marshal(copilotBlob{
			GitHubToken: credential.Access,
			APIEndpoint: credential.BaseURL,
		})
	default:
		// Keep the fields the SDK does not model, for an older San reading
		// the same store.
		var blob codexBlob
		previous, loadErr := storedCredential(key)
		if loadErr != nil {
			return loadErr
		}
		_ = json.Unmarshal([]byte(previous), &blob)
		blob.AccessToken = credential.Access
		blob.RefreshToken = credential.Refresh
		blob.ExpiresAt = credential.ExpiresAt
		raw, err = json.Marshal(blob)
	}
	if err != nil {
		return err
	}
	return store.Set(key, string(raw))
}

func (credentials) Delete(vendorID string) error {
	key, ok := storeKey(vendorID)
	if !ok {
		return nil
	}
	store := secret.Default()
	if store != nil {
		if err := store.Delete(key); err != nil {
			return err
		}
	}
	return os.Unsetenv(key)
}

func (c credentials) List() ([]string, error) {
	var out []string
	for vendorID := range signIns {
		if _, found, err := c.Load(vendorID); err == nil && found {
			out = append(out, vendorID)
		}
	}
	return out, nil
}

var _ auth.Store = credentials{}
var _ auth.RefreshLocker = credentials{}
