package app

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPConfigurationWaitsForWorkerCleanup(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("done")), nil)
	f.host.provider = acpModelsProvider{f.host.provider.(acpTestProvider)}
	id := f.session(t, t.TempDir())
	s := f.host.sessions[id]
	release := make(chan struct{})
	defer s.params.Tasks.Join()
	defer close(release)
	worker := s.params.Tasks.CreateAgentTask("worker", "test", "cleanup", context.Background(), func() {})
	s.params.Tasks.Run(func() { <-release })
	modelReq := protocol.SetSessionConfigOptionRequest{Value: protocol.SetSessionConfigOptionRequestValueIDVariant{SessionID: id, ConfigID: "model", Value: "other"}}
	modeReq := protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"}
	checkRejected := func() {
		t.Helper()
		var modelRes protocol.SetSessionConfigOptionResponse
		var modeRes protocol.SetSessionModeResponse
		for _, err := range []error{
			f.client.Call(f.ctx, "session/set_config_option", modelReq, &modelRes),
			f.client.Call(f.ctx, "session/set_mode", modeReq, &modeRes),
		} {
			if err == nil || !strings.Contains(err.Error(), "Session busy") {
				t.Fatalf("expected busy, got %v", err)
			}
		}
		if s.params.ModelID != "test" || s.mode != setting.ModeNormal || s.policy.CurrentMode() != setting.ModeNormal {
			t.Fatal("rejected configuration mutated live policy/model")
		}
		history, err := s.data.Store.Read(string(id))
		if err != nil {
			t.Fatal(err)
		}
		if history.Metadata.Model != "test" || history.Metadata.Mode == "auto-accept" {
			t.Fatal("rejected configuration mutated persisted state")
		}
	}
	checkRejected()
	worker.Complete(nil)
	checkRejected() // Display terminal is not execution-cleanup completion.
	release <- struct{}{}
	s.params.Tasks.Join()
	var modelRes protocol.SetSessionConfigOptionResponse
	var modeRes protocol.SetSessionModeResponse
	if err := f.client.Call(f.ctx, "session/set_config_option", modelReq, &modelRes); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Call(f.ctx, "session/set_mode", modeReq, &modeRes); err != nil {
		t.Fatal(err)
	}
	if s.params.ModelID != "other" || s.policy.CurrentMode() != setting.ModeAutoAccept {
		t.Fatal("idle configuration not applied")
	}
}

func TestACPConfigurationCannotWidenRunningWorkerPolicy(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var denied atomic.Bool
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if hasResult(req) {
			if workerRequest(req) {
				for _, msg := range req.Messages {
					for _, block := range msg.Content {
						if block.Type == ai.BlockToolResult && block.ToolResult.IsError {
							denied.Store(true)
						}
					}
				}
			}
			return aitest.Says("done")(ctx, req)
		}
		if workerRequest(req) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: `{"file_path":"result.txt","content":"must not write"}`})(ctx, req)
		}
		return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: `{"prompt":"WORKER_BRIEF","run_in_background":true}`})(ctx, req)
	})
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	cwd := t.TempDir()
	id := f.session(t, cwd)
	if _, err := f.prompt(id, "delegate"); err != nil {
		t.Fatal(err)
	}
	var response protocol.SetSessionModeResponse
	if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"}, &response); err == nil {
		t.Fatal("mode changed with live worker")
	}
	release <- struct{}{}
	f.host.sessions[id].params.Tasks.Join()
	if _, err := os.Stat(filepath.Join(cwd, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("worker write escaped original policy: %v", err)
	}
	if !denied.Load() {
		t.Fatal("worker did not observe denied tool result")
	}
}

func TestACPConfigurationExcludedDuringPrompt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(entered)
		<-release
		return aitest.Says("done")(ctx, req)
	})
	f := newACPFixture(t, driver, nil)
	f.host.provider = acpModelsProvider{f.host.provider.(acpTestProvider)}
	id := f.session(t, t.TempDir())
	done := make(chan error, 1)
	go func() { _, err := f.prompt(id, "hold"); done <- err }()
	<-entered
	var modelRes protocol.SetSessionConfigOptionResponse
	var modeRes protocol.SetSessionModeResponse
	modelErr := f.client.Call(f.ctx, "session/set_config_option", protocol.SetSessionConfigOptionRequest{Value: protocol.SetSessionConfigOptionRequestValueIDVariant{SessionID: id, ConfigID: "model", Value: "other"}}, &modelRes)
	modeErr := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"}, &modeRes)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{modelErr, modeErr} {
		if err == nil || !strings.Contains(err.Error(), "Session busy") {
			t.Fatalf("expected SDK busy, got %v", err)
		}
	}
	s := f.host.sessions[id]
	if s.params.ModelID != "test" || s.policy.CurrentMode() != setting.ModeNormal {
		t.Fatal("foreground configuration leaked")
	}
}
