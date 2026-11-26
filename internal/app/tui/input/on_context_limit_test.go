package input

import (
	"testing"

	"github.com/genai-io/san/internal/llm"
)

func TestContextLimitExplicitSuccessMessage(t *testing.T) {
	store := newProviderTestStore(t)
	if err := store.CacheModels(llm.OpenAI, llm.AuthAPIKey, []llm.ModelInfo{{ID: "m", ContextWindow: 10000, MaxOutput: 1000}}); err != nil {
		t.Fatal(err)
	}
	current := &llm.CurrentModelInfo{Provider: llm.OpenAI, ModelID: "m"}
	for _, tc := range []struct {
		args string
		want int
	}{{"20000 2000", 2000}, {"reset", 1000}, {"nonsense", 0}, {"100 100", 0}} {
		_, msg := setContextLimit(store, current, tc.args)
		if tc.want == 0 {
			if msg != nil {
				t.Fatalf("failed command emitted change: %+v", msg)
			}
			continue
		}
		if msg == nil || msg.ModelID != "m" || msg.Provider != string(llm.OpenAI) || msg.MaxTokens != tc.want {
			t.Fatalf("args=%s msg=%+v", tc.args, msg)
		}
	}
	if _, msg := setContextLimit(nil, current, "20000 2000"); msg != nil {
		t.Fatal("missing store emitted change")
	}
}
