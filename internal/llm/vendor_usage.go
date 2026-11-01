package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/auth"
)

// ChatGPTSubscription reports whether the selected route uses personal ChatGPT
// credentials. An OpenAI API key does not grant access to subscription limits.
func ChatGPTSubscription(current *CurrentModelInfo) bool {
	return current != nil && current.Provider == OpenAI && current.AuthMethod == AuthSubscription
}

// ChatGPTUsage adapts San's credential store to the SDK's account-usage API.
func ChatGPTUsage(ctx context.Context) (auth.CodexUsage, error) {
	credential, found, err := (credentials{}).Load(codexVendor)
	if err != nil || !found || credential.Access == "" {
		return auth.CodexUsage{}, fmt.Errorf("ChatGPT usage: sign-in unavailable")
	}
	client, err := auth.HTTPClient(codexVendor, credential, credentials{}, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return auth.CodexUsage{}, fmt.Errorf("ChatGPT usage: client unavailable")
	}
	return auth.FetchCodexUsage(ctx, client)
}
