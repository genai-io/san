package app

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPWorkerQuestions(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			var forms atomic.Int32
			driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				if hasResult(req) {
					return aitest.Says("done")(ctx, req)
				}
				if workerRequest(req) {
					return aitest.Asks(ai.ToolCall{ID: "child-question", Name: "AskUserQuestion", Input: `{"options":["A","B"]}`})(ctx, req)
				}
				return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","run_in_background":%t}`, background)})(ctx, req)
			})
			f := newACPFixtureOptions(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				return acpChoice("allow_once"), nil
			}, acpWriteTimeout, acpFormInit(), func(context.Context, protocol.CreateElicitationRequest) (protocol.CreateElicitationResponse, error) {
				forms.Add(1)
				return acpAccepted(`{"q0":"0"}`), nil
			})
			id := f.session(t, t.TempDir())
			if _, err := f.prompt(id, "delegate"); err != nil {
				t.Fatal(err)
			}
			f.host.sessions[id].params.Tasks.Join()
			want := int32(1)
			if background {
				want = 0
			}
			if forms.Load() != want {
				t.Fatalf("forms=%d want=%d", forms.Load(), want)
			}
			if f.host.sessions[id].input.current() != nil {
				t.Fatal("worker retained turn")
			}
		})
	}
}
