package acp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestACPFailedExtensionSetupPreservesSessionList(t *testing.T) {
	f := newACPFixture(t, aitest.New(), nil)
	cwd := t.TempDir()
	store, err := session.OpenStore(f.ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.Create(f.ctx, session.CreateOptions{InitialCWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	id := saved.ID()
	if err := saved.Close(); err != nil {
		t.Fatal(err)
	}
	offline := httptest.NewServer(http.NotFoundHandler())
	defer offline.Close()
	servers := []protocol.MCPServer{{Value: protocol.MCPServerHTTPVariant{Name: "offline", Type: "http", URL: offline.URL, Headers: []protocol.HTTPHeader{}}}}
	for range 2 {
		if _, err := f.client.CreateSession(f.ctx, protocol.NewSessionRequest{CWD: cwd, MCPServers: servers}); err == nil {
			t.Fatal("new unexpectedly succeeded")
		}
	}
	var response protocol.LoadSessionResponse
	load := protocol.LoadSessionRequest{CWD: cwd, SessionID: protocol.SessionID(id), MCPServers: servers}
	if err := f.client.Call(f.ctx, "session/load", load, &response); err == nil {
		t.Fatal("load unexpectedly succeeded")
	}
	page, err := store.List(f.ctx, session.ListQuery{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SessionID != id {
		t.Fatalf("sessions after failed setup: %+v", page.Items)
	}
	load.MCPServers = []protocol.MCPServer{}
	if err := f.client.Call(f.ctx, "session/load", load, &response); err != nil {
		t.Fatalf("retry saved session: %v", err)
	}
}

func TestACPMCPChangedCatalogBetweenPrompts(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "catalog", Version: "1"}, nil)
	add := func(name string) {
		server.AddTool(&sdkmcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			return &sdkmcp.CallToolResult{}, nil
		})
	}
	add("old")
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
	t.Cleanup(httpServer.Close)
	driver := aitest.New(aitest.Says("one"), aitest.Says("two"))
	f := newACPFixture(t, driver, nil)
	created, err := f.client.CreateSession(f.ctx, protocol.NewSessionRequest{CWD: t.TempDir(), MCPServers: []protocol.MCPServer{{Value: protocol.MCPServerHTTPVariant{Name: "catalog", Type: "http", URL: httpServer.URL, Headers: []protocol.HTTPHeader{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(want, absent string) {
		t.Helper()
		if _, err := f.prompt(created.SessionID, "list tools"); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range driver.Last().Tools {
			if s.Name == absent {
				t.Fatalf("unexpected tool %s", absent)
			}
			found = found || s.Name == want
		}
		if !found {
			t.Fatalf("missing tool %s", want)
		}
	}
	prompt("mcp__catalog__old", "mcp__catalog__new")
	manager := f.host.sessions[created.SessionID].mcp
	changed := make(chan struct{}, 1)
	manager.SetOnToolsChanged(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	server.RemoveTools("old")
	add("new")
	timeout := time.After(5 * time.Second)
	for {
		select {
		case <-changed:
			schemas := manager.ToolSchemas()
			if len(schemas) == 1 && schemas[0].Name == "mcp__catalog__new" {
				prompt("mcp__catalog__new", "mcp__catalog__old")
				return
			}
		case <-timeout:
			t.Fatal("MCP catalog change not received")
		}
	}
}
