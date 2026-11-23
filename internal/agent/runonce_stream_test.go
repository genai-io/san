package agent

import (
	"context"
	"iter"
	"strings"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
)

type runOnceProvider struct{ driver *aitest.Driver }

func (p runOnceProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}
func (runOnceProvider) Name() string { return "fake:api_key" }
func (runOnceProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "fake", ContextWindow: 200_000}}, nil
}

func TestRunOnceSendsMemoryPhrasingToMain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, mode := range []setting.MemoryRecallMode{setting.MemoryRecallFast, setting.MemoryRecallWait} {
		for _, input := range []string{
			"remember: use tabs",
			"update project memory style.md: use spaces",
			"forget project memory style.md",
		} {
			t.Run(string(mode)+"/"+input, func(t *testing.T) {
				provider := runOnceProvider{aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					if got := req.Messages[len(req.Messages)-1].Text(); !strings.Contains(got, input) {
						t.Errorf("main input = %q; want it to contain %q", got, input)
					}
					return aitest.Says("main response")(ctx, req)
				})}
				params := BuildParams{
					Provider: provider, ModelID: "fake", MaxTokens: 1024, CWD: t.TempDir(),
					AutoMemory: AutoMemoryConfig{Recall: setting.MemoryRecallConfig{Mode: mode}},
				}
				result, err := RunOnce(t.Context(), params, core.UserMessage(input, nil), nil)
				if err != nil || result == nil || result.Content != "main response" {
					t.Fatalf("RunOnce = %+v, %v; want main response", result, err)
				}
			})
		}
	}
}

func TestRunOnceStreamsPastDefaultOutboxCapacity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, mode := range []string{"streamed", "result-only"} {
		t.Run(mode, func(t *testing.T) {
			deltas := make([]ai.Delta, 128)
			for i := range deltas {
				deltas[i] = ai.Delta{Block: ai.Block{Type: ai.BlockText, Text: "x"}}
			}
			deltas = append(deltas, ai.Delta{StopReason: ai.StopEndTurn})
			provider := runOnceProvider{aitest.New(aitest.Streams(deltas...))}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			var output, observed strings.Builder
			var onText func(string)
			if mode == "streamed" {
				onText = func(text string) { output.WriteString(text) }
			}
			params := BuildParams{
				Provider: provider, ModelID: "fake", MaxTokens: 1024, CWD: t.TempDir(),
				OnEvent: func(ev core.Event) {
					if update, ok := ev.(sdkagent.InferenceUpdate); ok {
						observed.WriteString(update.Text())
					}
				},
			}
			result, err := RunOnce(ctx, params, core.UserMessage("answer", nil), onText)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Repeat("x", len(deltas)-1)
			if result == nil || result.Content != want {
				t.Fatalf("result = %+v; want %q", result, want)
			}
			if observed.String() != want {
				t.Fatalf("observer received %d bytes; want %d", observed.Len(), len(want))
			}
			if mode == "result-only" {
				want = ""
			}
			if output.String() != want {
				t.Fatalf("streamed %d bytes; want %d", output.Len(), len(want))
			}
		})
	}
}
