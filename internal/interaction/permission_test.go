package interaction

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func permissionRequest(ctx context.Context, owner Owner) (*Request, *perm.PermissionRequest) {
	return &Request{Context: ctx, Owner: owner, Permission: &agent.PermGateRequest{
		Context: ctx, RequestID: "approval-1", ToolName: "Write",
		Input: map[string]any{"file_path": "fixture"}, Response: make(chan agent.PermGateResponse, 1),
	}}, &perm.PermissionRequest{ID: "approval-1", ToolName: "Write", AllowRules: []string{"Write(fixture)"}}
}

func TestPermissionScopesAndAuditBeforeReply(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		approve, session, persist, worker, once bool
		scope                                   string
		grants                                  []string
	}{
		{name: "deny", scope: "once"},
		{name: "once", approve: true, scope: "once"},
		{name: "session", approve: true, session: true, scope: "session", grants: []string{"session:Write"}},
		{name: "persistent", approve: true, persist: true, scope: "persistent", grants: []string{"Write(fixture)"}},
		{name: "persistent precedence", approve: true, session: true, persist: true, scope: "persistent", grants: []string{"Write(fixture)"}},
		{name: "worker", approve: true, session: true, persist: true, worker: true, scope: "once"},
		{name: "safety confirmation", approve: true, session: true, persist: true, once: true, scope: "once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &testOwner{}
			req, view := permissionRequest(context.Background(), owner)
			view.OnceOnly = tc.once
			if tc.worker {
				req.Source = tool.AgentInteractionSource{AgentID: "worker", WorkingDirectory: "/worker", PermissionMode: "edit"}
			}
			var records []transcript.Record
			options := func(v *perm.PermissionRequest) []string {
				if v.OnceOnly != (tc.once || tc.worker) {
					t.Fatal("options built before scope restriction")
				}
				if v.OnceOnly {
					return []string{"Allow once", "Deny"}
				}
				return []string{"Allow once", "Allow session", "Always allow", "Deny"}
			}
			p := NewPermission(req, owner, view, options, func(r transcript.Record) {
				if len(req.Permission.Response) != 0 {
					t.Fatal("worker woke before audit")
				}
				records = append(records, r)
			})
			// Rendering changes cannot expand the captured grant scope or alter audit input.
			view.OnceOnly = false
			view.AllowRules = []string{"Write(**)"}
			req.Permission.Input["_onActivity"] = func() {}
			req.Permission.Input["file_path"] = "changed"
			var grants []string
			allow := func(name string) { grants = append(grants, "session:"+name) }
			persist := func(rules []string) { grants = append(grants, rules...) }
			d := PermissionDecision{Approved: tc.approve, AllowAll: tc.session, Persist: tc.persist, Request: view}
			if !p.Resolve(owner, d, allow, persist) || p.Resolve(owner, d, allow, persist) {
				t.Fatal("decision was not exactly once")
			}
			if got := <-req.Permission.Response; got.Allow != tc.approve {
				t.Fatal(got)
			}
			if !reflect.DeepEqual(grants, tc.grants) {
				t.Fatalf("grants=%v want %v", grants, tc.grants)
			}
			if len(records) != 2 || records[0].Type != transcript.PermissionRequired || records[1].Type != transcript.PermissionDecided {
				t.Fatalf("audit=%+v", records)
			}
			record := records[1].Permission
			if record.Scope != tc.scope || string(record.Input) != `{"file_path":"fixture"}` || record.RequestID != "approval-1" {
				t.Fatalf("record=%+v", record)
			}
			if tc.worker && (record.Mode != "edit" || len(record.Detail) == 0) {
				t.Fatal("worker context lost")
			}
		})
	}
}

func TestPermissionRejectsStaleAndUnsupportedReplies(t *testing.T) {
	for _, kind := range []string{"cancel", "closed", "replacement", "wrong view", "missing rules", "missing grant handler"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner := &testOwner{}
			req, view := permissionRequest(ctx, owner)
			if kind == "missing rules" {
				view.AllowRules = nil
			}
			records := 0
			p := NewPermission(req, owner, view, nil, func(transcript.Record) { records++ })
			d := PermissionDecision{Approved: true, Request: view}
			current := owner
			switch kind {
			case "cancel":
				cancel()
			case "closed":
				owner.closed.Store(true)
			case "replacement":
				current = &testOwner{}
			case "wrong view":
				d.Request = &perm.PermissionRequest{ID: view.ID}
			case "missing rules":
				d.Persist = true
			case "missing grant handler":
				d.AllowAll = true
			}
			persist := func([]string) { t.Fatal("invalid reply granted rules") }
			if p.Resolve(current, d, nil, persist) || records != 1 || len(req.Permission.Response) != 0 {
				t.Fatal("invalid reply changed state")
			}
		})
	}
}

func TestPermissionHookGateCancellationAndSuccessor(t *testing.T) {
	gate := agent.NewPermissionGate(func(string, map[string]any) agent.PermDecisionResult {
		return agent.PermDecisionResult{Decision: perm.Prompt, RequestID: "gate-request"}
	})
	defer gate.Close()
	owner := &testOwner{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run := func(ctx context.Context, id string) <-chan sdkagent.Decision {
		done := make(chan sdkagent.Decision, 1)
		go func() {
			d, _ := tool.HookedPermission(nil, gate)(core.WithToolCallID(ctx, id), sdkagent.PreToolContext{
				Call: ai.ToolCall{ID: id, Name: "Write", Input: `{"file_path":"fixture","content":"hello"}`},
			})
			done <- d
		}()
		return done
	}
	firstCtx, stopFirst := context.WithCancel(ctx)
	firstDone := run(firstCtx, "first")
	first, ok := gate.Recv()
	if !ok {
		t.Fatal("gate closed")
	}
	firstReq := &Request{Context: first.Context, Owner: owner, Permission: first}
	firstView := &perm.PermissionRequest{ToolName: "Write"}
	old := NewPermission(firstReq, owner, firstView, nil, nil)
	stopFirst()
	select {
	case d := <-firstDone:
		if !d.Block {
			t.Fatal("cancelled tool allowed")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	secondDone := run(ctx, "second")
	second, ok := gate.Recv()
	if !ok || second.ToolCallID != "second" {
		t.Fatal("successor identity lost")
	}
	secondReq := &Request{Context: second.Context, Owner: owner, Permission: second}
	secondView := &perm.PermissionRequest{ToolName: "Write"}
	audited := false
	next := NewPermission(secondReq, owner, secondView, nil, func(r transcript.Record) {
		if r.Type == transcript.PermissionDecided {
			audited = true
		}
	})
	if old.Resolve(owner, PermissionDecision{Approved: true, Request: firstView}, nil, nil) {
		t.Fatal("late reply authorized cancelled tool")
	}
	if next.Resolve(owner, PermissionDecision{Approved: true, Request: firstView}, nil, nil) {
		t.Fatal("old view authorized successor")
	}
	if !next.Resolve(owner, PermissionDecision{Approved: true, Request: secondView}, nil, nil) {
		t.Fatal("valid successor rejected")
	}
	select {
	case d := <-secondDone:
		if d.Block || !audited {
			t.Fatal("successor blocked or unaudited")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestPermissionCancellationDuringAuditFailsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &testOwner{}
	req, view := permissionRequest(ctx, owner)
	p := NewPermission(req, owner, view, nil, func(r transcript.Record) {
		if r.Type == transcript.PermissionDecided {
			cancel()
		}
	})
	if !p.Resolve(owner, PermissionDecision{Approved: true, Request: view}, nil, nil) {
		t.Fatal("decision not consumed")
	}
	if got := <-req.Permission.Response; got.Allow {
		t.Fatal("cancelled caller authorized")
	}
}
