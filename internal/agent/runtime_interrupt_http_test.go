package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/driver/openai/chat"
)

// The transport remains net/http. These wrappers only hold its return/Close
// after cancellation, to make premature execution completion observable.
type interruptHTTPTransport struct {
	base    http.RoundTripper
	exiting chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *interruptHTTPTransport) hold() { t.once.Do(func() { close(t.exiting); <-t.release }) }
func (t *interruptHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		t.hold()
	} else {
		resp.Body = &interruptHTTPBody{ReadCloser: resp.Body, hold: t.hold}
	}
	return resp, err
}

type interruptHTTPBody struct {
	io.ReadCloser
	hold func()
}

func (b *interruptHTTPBody) Close() error { err := b.ReadCloser.Close(); b.hold(); return err }

func TestRuntimeInterruptAbortsModelHTTP(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "before_headers"
		if streaming {
			name = "stalled_after_chunk"
		}
		t.Run(name, func(t *testing.T) {
			entered, aborted, chunk := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				// Consume the request so net/http can observe a peer disconnect.
				_, _ = io.Copy(io.Discard, r.Body)
				if n != 1 {
					http.Error(w, "unexpected successor", http.StatusInternalServerError)
					return
				}
				close(entered)
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"id\":\"first\",\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(aborted)
			}))
			defer func() {
				server.CloseClientConnections()
				server.Close()
			}()
			transport := &interruptHTTPTransport{base: server.Client().Transport, exiting: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(transport.release) }) }
			defer release()
			model := ai.Model{ID: "test", BaseURL: server.URL + "/"}
			driver, err := chat.New(ai.Config{Model: model, HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			client := ai.NewClientWithDriver(driver, model)
			ended := make(chan core.Result, 4)
			var chunkOnce sync.Once
			runtime := &Runtime{build: func(p BuildParams) (core.Agent, *PermissionGate, error) {
				return core.NewAgent(core.Config{
					ID: "http-cancel", System: core.NewSystem(), Tools: core.NewTools(),
					Client:                  func([]core.Message) (*ai.Client, error) { return client, nil },
					StreamFirstChunkTimeout: time.Minute, StreamIdleTimeout: time.Minute,
					OnEvent: func(ev core.Event) {
						p.OnEvent(ev)
						if e, ok := ev.(sdkagent.InferenceUpdate); ok && e.Delta.Type == ai.EventBlockDelta && e.Delta.Block.Text == "hello" {
							chunkOnce.Do(func() { close(chunk) })
						}
						if e, ok := ev.(core.TurnEnded); ok {
							ended <- e.Result
						}
					},
				}), nil, nil
			}}
			if err := runtime.Start(BuildParams{}, nil); err != nil {
				t.Fatal(err)
			}
			defer func() { release(); runtime.Stop() }()
			drained := make(chan struct{})
			go func() {
				for range runtime.Outbox() {
				}
				close(drained)
			}()
			wait := func(ch <-chan struct{}, what string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for %s", what)
				}
			}
			if err := runtime.Send(core.UserMessage("start", nil)); err != nil {
				t.Fatal(err)
			}
			wait(entered, "model request")
			if streaming {
				wait(chunk, "decoded first chunk")
			}
			done := runtime.InterruptCurrentTurn()
			wait(aborted, "server request context cancellation")
			wait(transport.exiting, "HTTP unwind")
			select {
			case <-done:
				t.Fatal("ThinkAct completed before HTTP stream exited")
			default:
			}
			if runtime.InterruptCurrentTurn() != done {
				t.Fatal("lost unwinding turn handle")
			}
			release()
			wait(done, "ThinkAct completion")
			select {
			case result := <-ended:
				if result.StopReason != core.StopCanceled {
					t.Fatalf("stop reason = %s", result.StopReason)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing TurnEnded")
			}
			// A cancelled turn must not enter retry backoff, continue partial output,
			// or start another exchange. Keep Run alive, without explicit new input.
			select {
			case result := <-ended:
				t.Fatalf("unexpected successor: %+v", result)
			case <-time.After(300 * time.Millisecond):
			}
			if got := requests.Load(); got != 1 {
				t.Fatalf("model requests = %d, want 1", got)
			}
			if !runtime.Active() {
				t.Fatal("turn interrupt stopped runtime")
			}
			runtime.Stop()
			wait(drained, "outbox shutdown")
		})
	}
}
