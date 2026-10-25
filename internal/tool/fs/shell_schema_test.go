package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/chat"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/responses"
)

// Inspect what reaches a provider, not just the source schema. Optional fields
// must stay nullable even when a strict endpoint requires callers to fill them.
func TestShellSchemaSentToProviders(t *testing.T) {
	for _, protocol := range []struct {
		name string
		new  func(ai.Config) (ai.Driver, error)
		sse  string
	}{
		{"chat", chat.New, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"responses", responses.New, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"test\",\"status\":\"completed\",\"output\":[]}}\n\n"},
	} {
		for _, shell := range []proc.Shell{{Kind: proc.ShellBash, Path: "bash"}, {Kind: proc.ShellPowerShell, Path: "pwsh"}} {
			t.Run(protocol.name+"/"+shell.Kind.ToolName(), func(t *testing.T) {
				captured := make(chan map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode provider request: %v", err)
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					captured <- body
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, protocol.sse)
				}))
				defer server.Close()
				driver, err := protocol.new(ai.Config{Model: ai.Model{ID: "fixture", BaseURL: server.URL + "/v1"}, APIKey: "local-test-only", HTTPClient: server.Client()})
				if err != nil {
					t.Fatal(err)
				}
				schema := (&ShellTool{shell: shell}).Schema()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, err := range driver.Stream(ctx, &ai.Request{Messages: []ai.Message{ai.UserMessage("Inspect the workspace")}, Tools: []ai.Schema{schema}}) {
					if err != nil {
						t.Fatal(err)
					}
				}
				var body map[string]any
				select {
				case body = <-captured:
				default:
					t.Fatal("provider did not receive the tool schema")
				}
				tools := body["tools"].([]any)
				if len(tools) != 1 {
					t.Fatalf("provider received %d tools", len(tools))
				}
				function := tools[0].(map[string]any)
				if protocol.name == "chat" {
					function = function["function"].(map[string]any)
				}
				if function["name"] != schema.Name {
					t.Fatalf("shell tool renamed in transport: %v", function["name"])
				}
				parameters := function["parameters"].(map[string]any)
				properties := parameters["properties"].(map[string]any)
				if len(properties) != 4 || !reflect.DeepEqual(parameters["required"], []any{"command"}) {
					t.Fatalf("unexpected model arguments: %v", parameters)
				}
				for _, field := range []string{"description", "timeout_ms", "execution_mode"} {
					property := properties[field].(map[string]any)
					if types, ok := property["type"].([]any); !ok || !slices.Contains(types, any("null")) {
						t.Fatalf("optional field %s lost null semantics: %v", field, property)
					}
				}
				mode := properties["execution_mode"].(map[string]any)
				if !reflect.DeepEqual(mode["enum"], []any{shellForeground, shellBackgroundJob, shellBackgroundService, nil}) {
					t.Fatalf("execution modes changed in transport: %v", mode)
				}
				for _, legacy := range []string{"timeout", "run_in_background", "lifetime"} {
					if _, exposed := properties[legacy]; exposed || strings.Contains(schema.Description, legacy+"=") {
						t.Fatalf("legacy argument %s still advertised to the model", legacy)
					}
				}
				if !strings.Contains(schema.Description, "foreground by default") || !strings.Contains(schema.Description, "independent next step") {
					t.Fatal("shell guidance lost the foreground default or the reason for background execution")
				}
			})
		}
	}
}
