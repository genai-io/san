package llm_test

import (
	"testing"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/llm/anthropic"
	"github.com/genai-io/san/internal/llm/deepseek"
	"github.com/genai-io/san/internal/llm/mimo"
	"github.com/genai-io/san/internal/llm/minmax"
	"github.com/genai-io/san/internal/llm/ollama"
	"github.com/genai-io/san/internal/llm/sensenova"
)

// TestStaticCatalogsHaveInputLimits is a guardrail: every model that a
// provider ships in its static catalog MUST declare an InputTokenLimit.
// A zero limit means the context-usage status bar can't render and
// auto-compact can't fire — both fail silently.
//
// When adding a new model to any catalog, set its InputTokenLimit.
// If the limit is genuinely unknown, omit the model from the static
// catalog and resolve it dynamically (API fetch or /tokenlimit).
func TestStaticCatalogsHaveInputLimits(t *testing.T) {
	providers := []struct {
		name   string
		models []llm.ModelInfo
	}{
		{"anthropic", anthropic.StaticModels()},
		{"deepseek", deepseek.StaticModels()},
		{"mimo", mimo.StaticModels()},
		{"minmax", minmax.StaticModels()},
		{"ollama", ollama.StaticModels()},
		{"sensenova", sensenova.StaticModels()},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			if len(p.models) == 0 {
				t.Fatalf("%s: StaticModels() returned empty", p.name)
			}
			for _, m := range p.models {
				if m.InputTokenLimit == 0 {
					t.Errorf("%s: model %q has InputTokenLimit=0; set the context window or remove from catalog", p.name, m.ID)
				}
			}
		})
	}
}
