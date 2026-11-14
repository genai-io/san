package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	_ "github.com/genai-io/san/internal/tool/register"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/chat"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/responses"
)

type schemaWireProvider struct {
	stubProvider
	client *ai.Client
}

func (p schemaWireProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}

// Admission and native persistence copy schema numbers through json.Number.
// Check the actual provider wire, where vendor encoders can mistake them for strings.
func TestRuntimeQuestionSchemaNumericWire(t *testing.T) {
	for _, protocol := range []struct {
		name string
		new  func(ai.Config) (ai.Driver, error)
		sse  string
	}{
		{"chat", chat.New, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"responses", responses.New, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"test\",\"status\":\"completed\",\"output\":[]}}\n\n"},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			owner, params, _ := resumeFixture(t)
			captured := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&body); err != nil {
					t.Errorf("decode provider request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				captured <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, protocol.sse)
			}))
			defer server.Close()
			model := ai.Model{ID: "fixture", BaseURL: server.URL + "/v1"}
			driver, err := protocol.new(ai.Config{Model: model, APIKey: "local-test-only", HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			params.Provider = schemaWireProvider{client: ai.NewClientWithDriver(driver, model)}
			params.ModelID = model.ID
			var runtime Runtime
			defer resumeStop(t, &runtime)
			if err := runtime.Start(params, nil); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"fresh", "resumed"} {
				t.Run(mode, func(t *testing.T) {
					if mode == "resumed" {
						resumeStop(t, &runtime)
						if _, err := runtime.Resume(params); err != nil {
							t.Fatal(err)
						}
					}
					if err := runtime.Send(core.UserMessage("Inspect workspace settings", nil)); err != nil {
						t.Fatal(err)
					}
					deadline := time.After(5 * time.Second)
				waitTurn:
					for {
						select {
						case event, ok := <-runtime.Outbox():
							if !ok {
								t.Fatal("runtime stopped before completing the provider turn")
							}
							if end, ok := event.(core.TurnEnded); ok {
								if end.Result.StopReason != core.StopEndTurn {
									t.Fatalf("turn failed: %+v", end.Result)
								}
								break waitTurn
							}
						case <-deadline:
							t.Fatal("timed out waiting for provider turn")
						}
					}
					if err := owner.Err(); err != nil {
						t.Fatal(err)
					}
					var body map[string]any
					select {
					case body = <-captured:
					default:
						t.Fatal("provider received no request")
					}
					for _, item := range body["tools"].([]any) {
						function := item.(map[string]any)
						if protocol.name == "chat" {
							function = function["function"].(map[string]any)
						}
						if function["name"] != tool.ToolAskUserQuestion {
							continue
						}
						parameters := function["parameters"].(map[string]any)
						for path, want := range map[string]string{
							"minProperties":                                          "1",
							"properties.options.minItems":                            "2",
							"properties.options.maxItems":                            "8",
							"properties.questions.minItems":                          "1",
							"properties.questions.maxItems":                          "8",
							"properties.questions.items.properties.options.minItems": "2",
							"properties.questions.items.properties.options.maxItems": "8",
						} {
							var value any = parameters
							for _, key := range strings.Split(path, ".") {
								value = value.(map[string]any)[key]
							}
							if number, ok := value.(json.Number); !ok || number.String() != want {
								t.Errorf("%s = %#v (%T), want JSON number %s", path, value, value, want)
							}
						}
						return
					}
					t.Fatal("AskUserQuestion missing from provider request")
				})
			}
		})
	}
}
