package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPLoadNativeStateAndModes(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("continued")), nil)
	cwd := t.TempDir()
	store, err := session.OpenStore(f.ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(f.ctx, session.CreateOptions{InitialCWD: cwd, Config: &session.EventConfig{Model: new("test")}})
	if err != nil {
		t.Fatal(err)
	}
	id := opened.ID()
	messages := []core.Message{core.UserMessage("remember the original", nil), core.AssistantMessage("remembered", "", nil)}
	for i := range messages {
		messages[i].ID = core.NewMessageID()
	}
	if err := opened.Append(f.ctx, session.Event{Payload: session.MessageAppendPayload{Messages: messages[:1]}}); err != nil {
		t.Fatal(err)
	}
	inference, err := opened.StartInference(f.ctx, session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "saved", Provider: "acp-test", Model: ai.Model{ID: "test"}, Request: &ai.Request{Messages: messages[:1]}})
	if err != nil {
		t.Fatal(err)
	}
	if err := inference.Finish(f.ctx, &ai.Response{Content: ai.TextContent("remembered")}, nil, session.Event{Payload: session.MessageAppendPayload{Messages: messages[1:]}}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Append(f.ctx, session.Event{Payload: session.SessionUpdatePayload{Config: &session.ConfigPatch{Mode: session.SetField("bypassPermissions")}}}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	var loaded protocol.LoadSessionResponse
	req := protocol.LoadSessionRequest{SessionID: protocol.SessionID(id), CWD: cwd, MCPServers: []protocol.MCPServer{}}
	if err := f.client.Call(f.ctx, "session/load", req, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Modes.Value.CurrentModeID != "normal" {
		t.Fatalf("restored unsafe mode: %+v", loaded.Modes)
	}
	f.host.mu.Lock()
	s := f.host.sessions[req.SessionID]
	f.host.mu.Unlock()
	if len(s.runtime.Messages()) != 2 || s.runtime.Messages()[0].Text() != "remember the original" {
		t.Fatal("native state not restored")
	}
	f.mu.Lock()
	count := len(f.updates)
	f.mu.Unlock()
	if count < 2 {
		t.Fatal("history was not replayed")
	}
	if err := f.client.Call(f.ctx, "session/load", req, &loaded); err == nil {
		t.Fatal("duplicate load admitted")
	}
	var modeResponse protocol.SetSessionModeResponse
	if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: req.SessionID, ModeID: "bypassPermissions"}, &modeResponse); err == nil {
		t.Fatal("bypass admitted")
	}
	if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: req.SessionID, ModeID: "auto-accept"}, &modeResponse); err != nil {
		t.Fatal(err)
	}
	if s.permissions.CurrentMode() != setting.ModeAutoAccept {
		t.Fatal("mode not applied")
	}
	if _, err := f.prompt(req.SessionID, "continue"); err != nil {
		t.Fatal(err)
	}
	history, err := store.Read(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Messages()) < 4 || *history.Config().Mode != "auto-accept" {
		t.Fatal("history/state not retained")
	}
}

type acpModelsProvider struct{ acpTestProvider }

func (acpModelsProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "test"}, {ID: "other"}}, nil
}

func TestACPModelSwitchPreservesOwners(t *testing.T) {
	f := newACPFixture(t, aitest.New(aitest.Says("one"), aitest.Says("two")), nil)
	f.host.provider = acpModelsProvider{f.host.provider.(acpTestProvider)}
	id := f.session(t, t.TempDir())
	if _, err := f.prompt(id, "first"); err != nil {
		t.Fatal(err)
	}
	s := f.host.sessions[id]
	permissions, views, skills := s.permissions, s.runtimeBuildParams.FileViews, s.runtimeBuildParams.Skills
	var res protocol.SetSessionConfigOptionResponse
	req := protocol.SetSessionConfigOptionRequest{Value: protocol.SetSessionConfigOptionRequestValueIDVariant{SessionID: id, ConfigID: "model", Value: "other"}}
	if err := f.client.Call(f.ctx, "session/set_config_option", req, &res); err != nil {
		t.Fatal(err)
	}
	if s.runtimeBuildParams.ModelID != "other" || s.permissions != permissions || s.runtimeBuildParams.FileViews != views || s.runtimeBuildParams.Skills != skills || len(s.runtime.Messages()) < 2 {
		t.Fatal("switch lost session owners")
	}
	history, err := s.nativeSession.Store.Read(f.ctx, string(id))
	if err != nil || *history.Config().Model != "other" {
		t.Fatalf("selection not durable: %v", err)
	}
	if _, err := f.prompt(id, "second"); err != nil {
		t.Fatal(err)
	}
}

func TestACPSkillUsesSessionRegistry(t *testing.T) {
	driver := aitest.New(aitest.Asks(core.ToolCall{ID: "skill-call", Name: "Skill", Input: `{"skill":"local"}`}), aitest.Says("loaded"))
	var approvals atomic.Int32
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		approvals.Add(1)
		return protocol.RequestPermissionResponse{Outcome: protocol.RequestPermissionOutcome{Value: protocol.RequestPermissionOutcomeSelectedVariant{Outcome: "selected", OptionID: "allow_once"}}}, nil
	})
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".san", "skills", "local")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: local\ndescription: local test\n---\nSESSION OWNED SKILL\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id := f.session(t, cwd)
	other := f.session(t, t.TempDir())
	if f.host.sessions[other].runtimeBuildParams.Skills.FindByPartialName("local") != nil {
		t.Fatal("skill leaked across sessions")
	}
	if _, err := f.prompt(id, "load the local skill"); err != nil {
		t.Fatal(err)
	}
	history, err := f.host.sessions[id].nativeSession.Store.Read(f.ctx, string(id))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range history.Messages() {
		for _, r := range m.ToolResults() {
			if strings.Contains(r.Content.Text(), "SESSION OWNED SKILL") {
				found = true
			}
		}
	}
	if !found || approvals.Load() != 1 {
		t.Fatalf("skill result=%v approvals=%d", found, approvals.Load())
	}
}

func TestACPClientMCPPermissionAndIsolation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(202)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "ping", "description": "fixture ping", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			calls.Add(1)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "MCP fixture result"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	var approvals atomic.Int32
	f := newACPFixture(t, aitest.New(aitest.Asks(core.ToolCall{ID: "mcp-call", Name: "mcp__fixture__ping", Input: `{}`}), aitest.Says("done")), func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		approvals.Add(1)
		return protocol.RequestPermissionResponse{Outcome: protocol.RequestPermissionOutcome{Value: protocol.RequestPermissionOutcomeSelectedVariant{Outcome: "selected", OptionID: "allow_once"}}}, nil
	})
	created, err := f.client.CreateSession(f.ctx, protocol.NewSessionRequest{CWD: t.TempDir(), MCPServers: []protocol.MCPServer{{Value: protocol.MCPServerHTTPVariant{Name: "fixture", Type: "http", URL: server.URL, Headers: []protocol.HTTPHeader{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	other := f.session(t, t.TempDir())
	if len(f.host.sessions[other].runtimeBuildParams.MCPTools) != 0 {
		t.Fatal("MCP tools leaked")
	}
	if _, err := f.prompt(created.SessionID, "ping"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || approvals.Load() != 1 {
		t.Fatalf("calls=%d approvals=%d", calls.Load(), approvals.Load())
	}
}
