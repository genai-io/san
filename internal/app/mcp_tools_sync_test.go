package app

import (
	"context"
	"slices"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/tests/integration/testutil"
)

type stubTool struct{ name string }

func (s stubTool) Schema() core.ToolSchema { return core.ToolSchema{Name: s.name} }
func (s stubTool) Run(context.Context, ai.ToolCall) (sdkagent.Result, error) {
	return sdkagent.TextResult("ok"), nil
}

// A server whose tools changed has to reach the agent that is running, not just
// the /mcp listing. The registry is what the loop reads at every step, so
// reconciling it is what lets the next step see the change — and until this was
// wired, Registry.SetOnToolsChanged had no caller outside a test, so the whole
// notification chain ended in a nil callback.
func TestSyncMCPToolsReachesTheRunningAgent(t *testing.T) {
	sess := &agent.Session{}
	if err := sess.Start(agent.BuildParams{Provider: &testutil.FakeProvider{}, ModelID: "m"}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Stop()

	tools := sess.Tools()
	if tools == nil {
		t.Fatal("a started session has no toolset")
	}
	tools.Add(stubTool{name: "mcp__gone__read"}, "mcp:test")
	tools.Add(stubTool{name: "Read"}, "builtin:test")

	m := &model{services: services{
		Agent:   sess,
		MCP:     mcp.NewRegistryForTest(nil),
		Setting: setting.Default(),
	}}
	m.syncMCPTools()

	var names []string
	for _, s := range tools.Schemas() {
		names = append(names, s.Name)
	}
	if slices.Contains(names, "mcp__gone__read") {
		t.Errorf("a tool no server advertises any more is still in the agent's registry: %v", names)
	}
	if !slices.Contains(names, "Read") {
		t.Errorf("reconciling the MCP tools took a built-in with it: %v", names)
	}
}

// No agent yet is not an error: whatever it is built with will be current.
func TestSyncMCPToolsBeforeAnAgentExists(t *testing.T) {
	m := &model{services: services{
		Agent:   &agent.Session{},
		MCP:     mcp.NewRegistryForTest(nil),
		Setting: setting.Default(),
	}}
	m.syncMCPTools()
}
