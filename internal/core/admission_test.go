package core

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type admissionTool struct{ run func(ai.ToolCall) }

func (admissionTool) Schema() ToolSchema { return noopTool{}.Schema() }
func (tool admissionTool) Run(_ context.Context, call ai.ToolCall) (sdkagent.Result, error) {
	tool.run(call)
	return sdkagent.TextResult("done"), nil
}

// Exercise the real SDK loop through core, not a second admission implementation.
func TestAgentAdmissionBoundaries(t *testing.T) {
	for _, fail := range []string{"", "inference", "tool", "model output", "tool output", "nil"} {
		t.Run(map[bool]string{true: "success", false: fail}[fail == ""], func(t *testing.T) {
			var order []string
			mark := func(s string) { order = append(order, s) }
			denied := errors.New("admission unavailable")
			admit := func(s string) error {
				mark(s)
				if fail == s {
					return denied
				}
				return nil
			}
			var ag Agent
			var admitted, published []Message
			cfg := Config{
				System: NewSystem(), OutboxBuf: -1, MaxSteps: 1,
				Client: testClient(aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					mark("model dispatch")
					return talks("answer")(ctx, req)
				})),
				PrepareMessages: func(msgs []Message) []Message {
					mark("prepare")
					return []Message{UserMessage("prepared", nil)}
				},
				Tools: NewTools(admissionTool{run: func(call ai.ToolCall) {
					mark("tool dispatch")
					if call.Input != `{"checked":true}` {
						t.Errorf("executed arguments = %q", call.Input)
					}
				}}),
				Gate: func(_ context.Context, c sdkagent.PreToolContext) (sdkagent.Decision, error) {
					mark("gate")
					return sdkagent.Decision{Arguments: `{"checked":true}`}, nil
				},
				ResultFilter: func(_ context.Context, c sdkagent.PostToolContext) (*sdkagent.Result, error) {
					mark("filter")
					result := sdkagent.TextResult("filtered")
					return &result, nil
				},
				OnEvent: func(e Event) {
					if added, ok := e.(sdkagent.MessageAdded); ok && (added.Message.Role != ai.RoleUser || len(added.Message.ToolResults()) > 0) {
						mark("published")
						published = append(published, added.Message)
					}
				},
			}
			if fail != "nil" {
				cfg.InferenceAdmission = func(_ context.Context, c sdkagent.InferenceAdmission) error {
					if c.Model.ID != "stub" || c.Attempt != 1 || len(c.Request.Messages) != 1 || c.Request.Messages[0].Content.Text() != "prepared" {
						t.Errorf("admission did not receive prepared request: %+v", c)
					}
					return admit("inference")
				}
				cfg.ToolAdmission = func(_ context.Context, c sdkagent.ToolAdmission) error {
					if c.Call.ID != "call-1" || c.Call.Input != `{"checked":true}` {
						t.Errorf("checked call = %+v", c.Call)
					}
					return admit("tool")
				}
				cfg.OutputAdmission = func(_ context.Context, c sdkagent.OutputAdmission) error {
					boundary := "model output"
					if c.Response == nil {
						boundary = "tool output"
						if results := c.Message.ToolResults(); len(results) != 1 || results[0].Text() != "filtered" || results[0].ToolCallID != "call-1" {
							t.Errorf("output did not receive filtered result: %+v", c.Message)
						}
					}
					if c.Message.ID == "" {
						t.Error("output has no ID")
					}
					for _, msg := range ag.Messages() {
						if msg.ID == c.Message.ID {
							t.Error("output already in history during admission")
						}
					}
					admitted = append(admitted, c.Message)
					return admit(boundary)
				}
			}
			ag = NewAgent(cfg)
			ag.Append(UserMessage("go", nil))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := ag.ThinkAct(ctx)
			want := []string{"prepare", "inference", "model dispatch", "model output", "published", "gate", "tool", "tool dispatch", "filter", "tool output", "published"}
			if fail == "nil" {
				want = []string{"prepare", "model dispatch", "published", "gate", "tool dispatch", "filter", "published"}
			} else if fail != "" {
				for i, s := range want {
					if s == fail {
						want = want[:i+1]
						break
					}
				}
			}
			if !reflect.DeepEqual(order, want) {
				t.Errorf("order = %v, want %v", order, want)
			}
			if fail != "" && fail != "nil" {
				var admissionErr *sdkagent.AdmissionError
				if !errors.Is(err, denied) || !errors.As(err, &admissionErr) || result.StopReason != StopError {
					t.Fatalf("result = %+v, error = %v; want fatal admission error", result, err)
				}
			} else if err != nil {
				t.Fatalf("ThinkAct: %v", err)
			}
			history := ag.Messages()
			if len(history) != 1+len(published) {
				t.Fatalf("history length = %d, published = %d", len(history), len(published))
			}
			for i, msg := range published {
				if msg.ID == "" || !reflect.DeepEqual(msg, history[i+1]) {
					t.Errorf("published message differs from history: %+v", msg)
				}
				if fail != "nil" && !reflect.DeepEqual(msg, admitted[i]) {
					t.Errorf("admitted message differs from publication: %+v", msg)
				}
			}
		})
	}
}
