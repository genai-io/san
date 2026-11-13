package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func workerPermissionOwner(t *testing.T, mode string) *session.Session {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent, err := store.Create(t.Context(), session.CreateOptions{ID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	child, err := store.Create(t.Context(), session.CreateOptions{ID: "child", AgentID: "worker-address", Parent: &session.ParentLink{Session: "parent", CallID: "launch-call"}, Config: &session.EventConfig{Mode: &mode}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	return child
}

func TestWorkerResolverPreservesModeSourceAndOnceOnlyApproval(t *testing.T) {
	child := workerPermissionOwner(t, "normal")
	settings := setting.New(setting.NewData())
	grants := setting.NewSessionPermissions()
	bridge := conv.NewAgentToUI(1)
	defer bridge.Close()
	origin := tool.WorkflowOrigin{TaskID: "workflow-run", Name: "review-pipeline", NodeID: "review", WorkerLabel: "item-7"}
	inputSource := tool.AgentInteractionSource{AgentID: "worker-address", TaskID: "executor-task", AgentName: "reviewer", ParentToolCallID: "launch-call", ToolCallID: "previous-call", WorkingDirectory: "/worker", PermissionMode: "normal", Workflow: &origin}
	expectedSource := inputSource
	expectedOrigin := origin
	expectedSource.Workflow = &expectedOrigin
	expectedSource.ToolCallID = "worker-call"
	resolver := newWorkerResolver(settings, grants, system.MemoryStores{}, bridge, child, inputSource)
	origin.Name = "changed after capture"
	grants.SetMode(setting.ModeBypassPermissions)
	ctx := core.WithToolCallID(t.Context(), "worker-call")
	done := make(chan perm.Resolution, 1)
	go func() { done <- resolver.Resolve(ctx, "Bash", map[string]any{"command": "go test ./..."}, false, "") }()
	msg := bridge.Check()()
	reqMsg, ok := msg.(conv.InteractionRequestMsg)
	if !ok {
		t.Fatalf("normal child silently inherited parent YOLO: %T", msg)
	}
	req := reqMsg.Request
	source := req.Source
	if !reflect.DeepEqual(source, expectedSource) {
		t.Fatalf("source=%+v", source)
	}
	view := req.PermissionView
	if !view.OnceOnly || len(view.AllowRules) != 0 {
		t.Fatal("worker approval scope expanded")
	}
	shown := interaction.NewPermission(req, bridge, view, func(*perm.PermissionRequest) []string { return []string{"Allow once", "Deny"} })
	if !shown.Resolve(bridge, interaction.PermissionDecision{Approved: true, AllowAll: true, Persist: true, Request: view}, nil, nil) {
		t.Fatal("reply rejected")
	}
	select {
	case got := <-done:
		if got.Decision != "allow" || got.By != "user" || got.Scope != "once" || got.Asked == nil {
			t.Fatalf("resolution=%+v", got)
		}
		for _, metadata := range []string{"executor-task", "reviewer", "workflow-run", "review-pipeline", "review", "item-7"} {
			if !strings.Contains(string(got.Asked.Detail), metadata) {
				t.Fatalf("shown workflow metadata %q missing: %s", metadata, got.Asked.Detail)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("worker remained blocked")
	}
}

func TestWorkerResolverPreservesPolicyAuthorityAndLiveGrants(t *testing.T) {
	child := workerPermissionOwner(t, "normal")
	data := setting.NewData()
	data.Permissions.Deny = []string{"Write(*)"}
	settings := setting.New(data)
	grants := setting.NewSessionPermissions()
	resolver := newWorkerResolver(settings, grants, system.MemoryStores{}, nil, child, tool.AgentInteractionSource{AgentID: "worker-address", TaskID: "executor-task", WorkingDirectory: "/worker", PermissionMode: "normal"})
	grants.AllowPattern("Bash(go test ./...)")
	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"Write", map[string]any{"file_path": "a"}},
		{"Bash", map[string]any{"command": "go test ./..."}},
	} {
		expected := (agent.PermissionPolicy{Settings: settings, Session: grants}).Decide(tc.name, tc.input)
		got := resolver.Resolve(t.Context(), tc.name, tc.input, false, "")
		decision := "deny"
		if expected.Decision == perm.Permit {
			decision = "allow"
		}
		if got.Decision != decision || got.By != expected.By || got.Scope != expected.Scope {
			t.Fatalf("%s resolution=%+v expected=%+v", tc.name, got, expected)
		}
	}
}
