//go:build unix

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPWorkerShellInput(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, secret := range []bool{false, true} {
			t.Run(fmt.Sprintf("background=%t/secret=%t", background, secret), func(t *testing.T) {
				command := `read -p "Continue? [y/N] " a; echo "answer=$a"`
				if secret {
					command = `read -s -p "Password: " a; echo refused`
				}
				args, _ := json.Marshal(map[string]any{"command": command, "timeout_ms": 3000})
				var answered atomic.Bool
				driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					if hasResult(req) {
						encoded, _ := json.Marshal(req)
						if workerRequest(req) && strings.Contains(string(encoded), "answer=yes") {
							answered.Store(true)
						}
						return aitest.Says("done")(ctx, req)
					}
					if workerRequest(req) {
						return aitest.Asks(ai.ToolCall{ID: "shell", Name: "Bash", Input: string(args)})(ctx, req)
					}
					return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","run_in_background":%t}`, background)})(ctx, req)
				})
				var forms atomic.Int32
				f := newACPFixtureOptions(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
					return acpChoice("allow_once"), nil
				}, acpWriteTimeout, acpFormInit(), func(context.Context, protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
					forms.Add(1)
					return acpAccepted(`{"answer":"yes"}`), nil
				})
				id := f.session(t, t.TempDir())
				if _, err := f.prompt(id, "delegate"); err != nil {
					t.Fatal(err)
				}
				f.host.sessions[id].runtimeBuildParams.Tasks.Join()
				want := int32(0)
				if !background && !secret {
					want = 1
					if !answered.Load() {
						t.Error("worker did not receive shell answer")
					}
				}
				if forms.Load() != want {
					t.Fatalf("forms=%d want=%d", forms.Load(), want)
				}
			})
		}
	}
}

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
