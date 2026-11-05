//go:build unix

package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPShellInput(t *testing.T) {
	for _, secret := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "secret refused"}[secret], func(t *testing.T) {
			command := `read -p "Continue? [y/N] " a; echo "answer=$a"`
			want := "answer=yes"
			if secret {
				command = `if read -s -p "Password: " a; then echo unexpected; else echo refused; fi`
				want = "refused"
			}
			args, _ := json.Marshal(map[string]any{"command": command, "timeout_ms": 3000})
			driver := aitest.New(aitest.Asks(ai.ToolCall{ID: "shell", Name: "Bash", Input: string(args)}), aitest.Says("done"))
			var forms atomic.Int32
			f := newACPFixtureOptions(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				return acpChoice("allow_once"), nil
			}, acpWriteTimeout, acpFormInit(), func(_ context.Context, req protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
				forms.Add(1)
				if secret {
					t.Error("secret prompt was exposed as plain form")
				}
				return acpAccepted(`{"answer":"yes"}`), nil
			})
			id := f.session(t, t.TempDir())
			if _, err := f.prompt(id, "run"); err != nil {
				t.Fatal(err)
			}
			sent, _ := json.Marshal(driver.Sent())
			if !strings.Contains(string(sent), want) {
				t.Fatalf("missing output %q", want)
			}
			expected := int32(1)
			if secret {
				expected = 0
			}
			if forms.Load() != expected {
				t.Fatalf("forms=%d", forms.Load())
			}
		})
	}
}
