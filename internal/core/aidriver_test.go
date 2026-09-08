package core

import (
	"github.com/genai-io/sdk-go/pkg/ai"
)

// Faking the model means faking the protocol, which is the seam pkg/ai already
// has and the one five real drivers sit behind. A test says what the endpoint
// sends; nothing in between has to be stubbed. The double itself is the SDK's
// (pkg/ai/aitest) — this only wraps one as the per-turn client San asks for.

// testClient wraps a driver as the per-turn client an agent is built on. The
// same client answers every turn: only a real endpoint varies its headers with
// what the turn sends.
func testClient(d ai.Driver) func([]Message) (*ai.Client, error) {
	client := ai.NewClientWithDriver(d, ai.Model{ID: "stub", API: "stub", ContextWindow: 200_000})
	return func([]Message) (*ai.Client, error) { return client, nil }
}
