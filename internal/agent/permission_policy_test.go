package agent

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/reviewer"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/transcript"
)

func TestPermissionPolicyRules(t *testing.T) {
	data := setting.NewData()
	session := &setting.SessionPermissions{Mode: setting.ModeAutoPilot}
	var records []transcript.Record
	p := PermissionPolicy{Settings: data, Session: session, Audit: func(r transcript.Record) { records = append(records, r) }}
	args := map[string]any{"command": "go test ./..."}
	first, second := p.Decide("Bash", args), p.Decide("Bash", args)
	if first.Decision != perm.Prompt || !first.Reviewable || first.RequestID == "" || first.RequestID == second.RequestID || len(records) != 0 {
		t.Fatalf("prompt metadata: %+v %+v, records %v", first, second, records)
	}
	data.Permissions.Deny = []string{"Bash"}
	if d := p.Decide("Bash", args); d.Decision != perm.Reject {
		t.Fatalf("deny: %+v", d)
	}
	if len(records) != 1 || records[0].Permission.Decision != "reject" || records[0].Permission.Source != transcript.PermissionSourceConfig || records[0].Permission.Mode != session.CurrentMode().PersistenceName() {
		t.Fatalf("audit: %+v", records)
	}
	if p.CanApproveByHook("Bash", args) {
		t.Fatal("hook bypassed deny")
	}
	data.Permissions.Deny = nil
	data.Permissions.Ask = []string{"Bash"}
	if p.CanApproveByHook("Bash", args) {
		t.Fatal("hook bypassed explicit ask")
	}
	data.Permissions.Ask = nil
	if !p.CanApproveByHook("Bash", args) {
		t.Fatal("routine hook allow rejected")
	}
	if p.CanApproveByHook("Bash", map[string]any{"command": "rm -rf /"}) {
		t.Fatal("hook bypassed breaker")
	}
	session.ShouldAvoidPrompts = true
	if d := p.Decide("Bash", args); d.Decision != perm.Reject {
		t.Fatalf("headless prompt: %+v", d)
	}
	if d := (PermissionPolicy{}).Decide("Read", nil); d.Decision != perm.Reject {
		t.Fatal("missing policy allowed")
	}
	if (PermissionPolicy{}).CanApproveByHook("Read", nil) {
		t.Fatal("missing policy honored hook")
	}
}

func TestPermissionPolicyMemoryAndAuditSnapshot(t *testing.T) {
	data := setting.NewData()
	dir := t.TempDir()
	session := &setting.SessionPermissions{ShouldAvoidPrompts: true}
	var records []transcript.Record
	p := PermissionPolicy{Settings: data, Session: session, AutoMemory: system.MemoryStores{Project: dir},
		Audit: func(r transcript.Record) { records = append(records, r) }}
	path := filepath.Join(dir, "a.md")
	args := map[string]any{"file_path": path}
	if d := p.Decide("Write", args); d.Decision != perm.Permit || d.Reason != "auto memory" {
		t.Fatalf("memory: %+v", d)
	}
	original := string(records[0].Permission.Input)
	args["file_path"] = "changed"
	if string(records[0].Permission.Input) != original {
		t.Fatal("audit aliases input")
	}
	args["file_path"] = path
	data.Permissions.Deny = []string{"Write(**/a.md)"}
	if d := p.Decide("Write", args); d.Decision != perm.Reject {
		t.Fatalf("memory bypassed deny: %+v", d)
	}
}

func TestPermissionPolicyReview(t *testing.T) {
	for _, outcome := range []string{"allow", "deny", "error", "cancel", "missing", "disabled", "disengaged", "breaker", "unrecoverable"} {
		t.Run(outcome, func(t *testing.T) {
			data := setting.NewData()
			session := &setting.SessionPermissions{Mode: setting.ModeAutoPilot}
			successor := &setting.SessionPermissions{Mode: setting.ModeAutoPilot}
			var order []string
			p := PermissionPolicy{Settings: data, Session: session, Audit: func(r transcript.Record) {
				order = append(order, "audit")
				if r.Permission.Source != transcript.PermissionSourceReviewer || r.Permission.Decision != "permit" {
					t.Fatalf("audit: %+v", r.Permission)
				}
			}}
			args := map[string]any{"command": "go test ./..."}
			if outcome == "breaker" {
				args["command"] = "rm -rf /"
			}
			if outcome == "unrecoverable" {
				args["command"] = "rm -rf /tmp/cache"
			}
			ctx, cancel := context.WithCancel(core.WithToolCallID(context.Background(), "call-1"))
			defer cancel()
			calls := 0
			r := PermissionReview{Engaged: outcome != "disengaged", Enabled: outcome != "disabled", CWD: "/workspace", UnderGit: true, Mission: "verify",
				Observe: func(ctx context.Context, allow bool, reason string) {
					order = append(order, "observe")
					if core.ToolCallIDFromContext(ctx) != "call-1" || reason == "" || allow != (outcome == "allow") {
						t.Fatal("invalid observation")
					}
				},
				Judge: func(ctx context.Context, req reviewer.Request) (reviewer.Verdict, error) {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 30*time.Second || core.ToolCallIDFromContext(ctx) != "call-1" {
						t.Fatal("missing review context/budget")
					}
					if req.CWD != "/workspace" || !req.UnderGit || req.Mission != "verify" || req.Reason != "gray zone" {
						t.Fatalf("request: %+v", req)
					}
					if outcome == "cancel" {
						cancel()
					}
					if outcome == "error" {
						return reviewer.Verdict{Allow: true}, errors.New("offline")
					}
					return reviewer.Verdict{Allow: outcome != "deny", Reason: "judgment"}, nil
				},
			}
			if outcome == "missing" {
				r.Judge = nil
			}
			got := p.Review(ctx, "Bash", args, "gray zone", r)
			if got.Allow != (outcome == "allow") {
				t.Fatalf("result: %+v", got)
			}
			if outcome == "allow" {
				if !reflect.DeepEqual(order, []string{"audit", "observe"}) {
					t.Fatalf("order: %v", order)
				}
				p.Audit = nil
				if d := p.Decide("Bash", args); d.Decision != perm.Permit {
					t.Fatalf("exact grant missing: %+v", d)
				}
				if d := p.Decide("Bash", map[string]any{"command": "go test ./other"}); d.Decision == perm.Permit {
					t.Fatal("grant broadened")
				}
				if d := data.HasPermissionToUseTool("Bash", args, successor); d.Behavior == perm.Permit {
					t.Fatal("grant crossed session lifetime")
				}
			} else {
				if len(order) > 0 && order[0] == "audit" {
					t.Fatal("failed review audited approval")
				}
				if len(session.Snapshot().AllowedPatterns) != 0 {
					t.Fatal("failed review created grants")
				}
			}
			if (outcome == "disabled" || outcome == "disengaged" || outcome == "breaker" || outcome == "unrecoverable") && calls != 0 {
				t.Fatal("guard called judge")
			}
		})
	}
}

func TestPermissionPolicyGateEscalatesReviewFailure(t *testing.T) {
	p := PermissionPolicy{Settings: setting.NewData(), Session: &setting.SessionPermissions{Mode: setting.ModeAutoPilot}}
	gate := NewPermissionGate(p.Decide)
	defer gate.Close()
	gate.SetReviewer(func(ctx context.Context, name string, args map[string]any, reason string) PermReviewResult {
		return p.Review(ctx, name, args, reason, PermissionReview{Engaged: true, Enabled: true})
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan bool, 1)
	go func() {
		allow, _ := gate.Check(ctx, "Bash", map[string]any{"command": "go test ./..."}, false, "")
		result <- allow
	}()
	req, ok := gate.Recv()
	if !ok || req.RequestID == "" {
		t.Fatal("missing human fallback")
	}
	req.Response <- PermGateResponse{Allow: false, Reason: "no"}
	if <-result {
		t.Fatal("failed review bypassed human")
	}
}
