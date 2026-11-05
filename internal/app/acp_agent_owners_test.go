package app

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPAgentSkillAndResponseOwners(t *testing.T) {
	owners := []string{"FIRST_OWNER", "SECOND_OWNER", "NO_SKILL"}
	seen := make(chan string, len(owners))
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		owner := ""
		for _, msg := range req.Messages {
			for _, candidate := range owners {
				if strings.Contains(msg.Content.Text(), candidate) {
					owner = candidate
				}
			}
		}
		if workerRequest(req) {
			seen <- owner
			for _, other := range owners[:2] {
				if strings.Contains(req.System, "PRELOADED_"+other) != (owner == other) {
					t.Errorf("wrong preloaded skill for %s: %s", owner, req.System)
				}
			}
			return aitest.Says(owner)(ctx, req)
		}
		if hasResult(req) {
			return aitest.Says("done")(ctx, req)
		}
		return aitest.Asks(ai.ToolCall{ID: "shared-agent-call", Name: "Agent", Input: fmt.Sprintf(`{"name":"local-worker","prompt":%q}`, "WORKER_BRIEF "+owner)})(ctx, req)
	})
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	ids := make(map[string]protocol.SessionID)
	responses := make(map[string]chan any)
	// All registries and response owners coexist on one host, with the same agent
	// definition, skill name and model call ID in every session.
	for _, owner := range owners {
		cwd := t.TempDir()
		files := map[string]string{".san/agents/local-worker.md": "---\nname: local-worker\ndescription: local worker\nskills:\n  - local\n---\nDo the requested task.\n"}
		if owner != "NO_SKILL" {
			files[".san/skills/local/SKILL.md"] = fmt.Sprintf("---\nname: local\ndescription: local skill\n---\nPRELOADED_%s\n", owner)
		}
		for path, body := range files {
			path = filepath.Join(cwd, path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		id := f.session(t, cwd)
		ids[owner] = id
		response := make(chan any, 1)
		responses[owner] = response
		f.host.sessions[id].hooks.AddSessionFunctionHook(hook.PostToolUse, "Agent", hook.FunctionHook{Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
			response <- in.ToolResponse
			return hook.HookOutput{}, nil
		}})
	}
	for _, owner := range owners {
		if _, err := f.prompt(ids[owner], owner); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-seen:
			if got != owner {
				t.Fatalf("wrong worker owner %s", got)
			}
		default:
			t.Fatal("worker never requested provider")
		}
		select {
		case response := <-responses[owner]:
			structured, ok := response.(map[string]any)
			if !ok || structured["content"] != owner || structured["status"] != "completed" {
				t.Fatalf("wrong structured response: %#v", response)
			}
		default:
			t.Fatal("PostToolUse did not receive Agent response")
		}
		if tool.PopSideEffect("shared-agent-call") != nil {
			t.Fatal("Agent response leaked into global store")
		}
	}
}
