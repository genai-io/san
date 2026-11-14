package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/tests/integration/testutil"
)

func namedTool(name string) core.Tool {
	return sdkagent.ToolFunc(name, "", func(context.Context, struct{}) (sdkagent.Result, error) {
		return sdkagent.TextResult("ok"), nil
	})
}

// A server whose tools changed has to reach the agent that is running, not just
// the /mcp listing. The registry is what the loop reads at every step, so
// reconciling it is what lets the next step see the change — and until this was
// wired, Registry.SetOnToolsChanged had no caller outside a test, so the whole
// notification chain ended in a nil callback.
func TestSyncMCPToolsReachesTheRunningAgent(t *testing.T) {
	sess := &agent.Runtime{}
	if err := sess.Start(agent.BuildParams{Provider: &testutil.FakeProvider{}, ModelID: "m"}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer stopRuntime(t, sess)

	tools := sess.Tools()
	if tools == nil {
		t.Fatal("a started session has no toolset")
	}
	tools.Add(namedTool("mcp__gone__read"), "mcp:test")
	tools.Add(namedTool("Read"), "builtin:test")

	m := &model{services: services{
		Agent:   sess,
		MCP:     mcp.NewManagerForTest(nil),
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

// Discovery may precede runtime startup.
func TestSyncMCPToolsBeforeAnAgentExists(t *testing.T) {
	for _, runtime := range []*agent.Runtime{nil, new(agent.Runtime)} {
		m := &model{services: services{
			Agent: runtime, MCP: mcp.NewManagerForTest(nil), Setting: setting.Default(),
		}}
		m.syncMCPTools()
	}
}

func TestMCPNotificationsReconcileOnTheUILoop(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "reload-test", Version: "1"}, nil)
	addTool := func(name string) {
		server.AddTool(&sdkmcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, nil
		})
	}
	addTool("probe")
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
	defer httpServer.Close()
	manager := mcp.NewManagerForTest(map[string]mcp.ServerConfig{"reload": {Type: mcp.TransportHTTP, URL: httpServer.URL}})
	defer func() { _ = manager.Close() }()
	runtime := &agent.Runtime{}
	if err := runtime.Start(agent.BuildParams{Provider: &testutil.FakeProvider{}, ModelID: "m"}, nil); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, runtime)
	m := &model{services: services{Agent: runtime, MCP: manager, Setting: setting.New(setting.NewData())}}
	m.bindMCPTools()
	if err := manager.Connect(t.Context(), "reload"); err != nil {
		t.Fatal(err)
	}
	if runtime.Tools().Get("mcp__reload__probe") != nil {
		t.Fatal("MCP callback mutated the live toolset outside the UI loop")
	}
	consume := func() {
		t.Helper()
		select {
		case <-m.mcpToolsChanged:
			m.dispatch(mcpToolsChangedMsg{})
		case <-time.After(5 * time.Second):
			t.Fatal("MCP tool notification did not reach the UI")
		}
	}
	consume()
	if runtime.Tools().Get("mcp__reload__probe") == nil {
		t.Fatal("connected tools were not reconciled")
	}
	addTool("later")
	consume()
	if runtime.Tools().Get("mcp__reload__later") == nil {
		t.Fatal("dynamic tools were not reconciled")
	}
}
