package agent

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func resumeFixture(t *testing.T) (*session.Session, BuildParams, *aitest.Driver) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	store, err := session.OpenStore(t.Context(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	driver := aitest.Always(aitest.Says("done"))
	return owner, BuildParams{Recording: owner, CWD: cwd, Provider: runOnceProvider{driver}, ModelID: "fake", Skills: skill.NewRegistry(), PermissionRules: func(string, map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Reject, By: session.ByConfig, Reason: "current deny"}
	}}, driver
}

func resumeStop(t *testing.T, rt *Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestResumeActiveContextAndUnfinished(t *testing.T) {
	owner, params, driver := resumeFixture(t)
	old := core.UserMessage("old", nil)
	old.ID = "old"
	if err := owner.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{old}}}); err != nil {
		t.Fatal(err)
	}
	summary := core.UserMessage("summary", nil)
	summary.ID = "summary"
	if err := owner.Append(t.Context(), session.Event{Payload: session.ContextCompactPayload{Summary: summary, Through: 2}}); err != nil {
		t.Fatal(err)
	}
	inference, err := owner.StartInference(t.Context(), session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "interrupted", Provider: "fake", Model: ai.Model{ID: "fake"}, Request: &ai.Request{Messages: []ai.Message{summary}}})
	if err != nil {
		t.Fatal(err)
	}
	calls := ai.Message{ID: "calls", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "never", Name: "Read", Input: `{}`}), ai.ToolCallBlock(ai.ToolCall{ID: "unknown", Name: "Read", Input: `{}`})}}
	ref := inference.Ref()
	if err := owner.Append(t.Context(), session.Event{Source: &ref, Payload: session.MessageAppendPayload{Messages: []ai.Message{calls}}}, session.Event{Payload: session.PermissionDecidePayload{CallID: "unknown", Decision: "allow", By: session.ByGrant}}); err != nil {
		t.Fatal(err)
	}
	before := owner.Snapshot()
	var rt Runtime
	defer resumeStop(t, &rt)
	unfinished, err := rt.Resume(params)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unfinished, before.Unfinished()) || len(unfinished.Inferences) != 1 || len(unfinished.NeverRan) != 1 || len(unfinished.Unknown) != 1 {
		t.Fatalf("unfinished: %+v", unfinished)
	}
	if !reflect.DeepEqual(rt.Messages(), before.ActiveMessages()) {
		t.Fatal("resume did not preserve exact active context")
	}
	resumeStop(t, &rt)
	after := owner.Snapshot()
	if driver.Calls() != 0 || !reflect.DeepEqual(after.Unfinished(), unfinished) || !reflect.DeepEqual(after.Messages(), before.Messages()) {
		t.Fatal("resume executed or fabricated work")
	}
	count := len(after.Events())
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	resumeStop(t, &rt)
	after = owner.Snapshot()
	if len(after.Events()) != count {
		t.Fatal("unchanged rebuild wrote events")
	}
}

func TestResumeRejectsWithoutReplacingInactiveChain(t *testing.T) {
	for _, mode := range []string{"identity", "closed", "cwd", "missing-owner"} {
		t.Run(mode, func(t *testing.T) {
			owner, params, driver := resumeFixture(t)
			var rt Runtime
			defer resumeStop(t, &rt)
			seed := []core.Message{core.UserMessage("retained", nil)}
			rt.SetMessages(seed)
			switch mode {
			case "identity":
				params.SessionID = "other"
			case "closed":
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			case "cwd":
				params.CWD = t.TempDir()
			case "missing-owner":
				params.Recording = nil
			}
			if _, err := rt.Resume(params); err == nil {
				t.Fatal("accepted invalid resume")
			}
			if rt.Active() || !reflect.DeepEqual(rt.Messages(), seed) || driver.Calls() != 0 {
				t.Fatal("failed resume changed inactive chain")
			}
		})
	}
}

func TestResumeActiveRejectsReplacement(t *testing.T) {
	_, params, _ := resumeFixture(t)
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	defer resumeStop(t, &rt)
	outbox := rt.Outbox()
	if _, err := rt.Resume(params); err == nil {
		t.Fatal("accepted active resume")
	}
	if rt.Outbox() != outbox {
		t.Fatal("replaced active generation")
	}
}

func TestResumeCatalogueRemovals(t *testing.T) {
	owner, params, _ := resumeFixture(t)
	if err := session.NewRecorder(owner, "fake").RecordCatalogues(t.Context(), []session.NamedInstruction{{Name: "obsolete", InstructionSection: session.InstructionSection{Content: "old"}}}, []ai.Schema{{Name: "obsolete"}}, map[string]string{"obsolete": session.SkillActive}, "boot"); err != nil {
		t.Fatal(err)
	}
	before := owner.Snapshot()
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	resumeStop(t, &rt)
	after := owner.Snapshot()
	found := map[string]bool{}
	for _, event := range after.Events()[len(before.Events()):] {
		var cause string
		var remove []string
		switch v := event.Payload.(type) {
		case *session.InstructionsUpdatePayload:
			cause, remove = v.Cause, v.Remove
			found["instructions"] = true
		case *session.ToolsetUpdatePayload:
			cause, remove = v.Cause, v.Remove
			found["tools"] = true
		case *session.SkillsUpdatePayload:
			cause, remove = v.Cause, v.Remove
			found["skills"] = true
		default:
			t.Fatalf("unexpected event %T", v)
		}
		if cause != "resume" || !reflect.DeepEqual(remove, []string{"obsolete"}) {
			t.Fatalf("cause/removal: %q %v", cause, remove)
		}
	}
	if len(found) != 3 {
		t.Fatalf("catalogues: %v", found)
	}
}

func TestResumeDispatchStorageFailure(t *testing.T) {
	owner, params, driver := resumeFixture(t)
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rt.Send(core.UserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-rt.Outbox():
			if end, ok := event.(sdkagent.TurnEnd); ok {
				if end.Err == nil {
					t.Fatal("missing terminal recording error")
				}
				resumeStop(t, &rt)
				if driver.Calls() != 0 {
					t.Fatal("dispatched without durable admission")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no turn completion")
		}
	}
}

func TestResumeCurrentDenyRules(t *testing.T) {
	owner, params, _ := resumeFixture(t)
	// An old grant remains audit-only, even for the same tool name.
	old := ai.Message{ID: "old-call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "old", Name: "ResumeFixture", Input: `{}`})}}
	inference, err := owner.StartInference(t.Context(), session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: "old", Provider: "fake", Model: ai.Model{ID: "fake"}, Request: &ai.Request{}})
	if err != nil {
		t.Fatal(err)
	}
	ref := inference.Ref()
	if err := owner.Append(t.Context(), session.Event{Source: &ref, Payload: session.MessageAppendPayload{Messages: []ai.Message{old}}}, session.Event{Payload: session.PermissionDecidePayload{CallID: "old", Decision: "allow", By: session.ByGrant}}); err != nil {
		t.Fatal(err)
	}
	ran := false
	params.NativeTools = []core.Tool{sdkagent.ToolFunc("ResumeFixture", "fixture", func(context.Context, struct{}) (sdkagent.Result, error) {
		ran = true
		return sdkagent.TextResult("bad"), nil
	})}
	driver := aitest.New(aitest.Replies(ai.Response{Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "new", Name: "ResumeFixture", Input: `{}`})}, StopReason: ai.StopToolUse}), aitest.Says("done"))
	params.Provider = runOnceProvider{driver}
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	if err := rt.Send(core.UserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-rt.Outbox():
			if _, ok := event.(core.TurnEnded); ok {
				resumeStop(t, &rt)
				if ran {
					t.Fatal("historical grant bypassed current deny")
				}
				p := owner.Snapshot()
				denied := false
				for _, event := range p.Events() {
					if decision, ok := event.Payload.(*session.PermissionDecidePayload); ok && decision.CallID == "new" {
						denied = decision.Decision == "deny" && decision.By == session.ByConfig
					}
				}
				if !denied {
					t.Fatal("current deny not durably recorded")
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no turn completion")
		}
	}
}

func TestResumeSavedExecutionDirectory(t *testing.T) {
	owner, params, _ := resumeFixture(t)
	initial := params.CWD
	saved := t.TempDir()
	if err := owner.Append(t.Context(), session.Event{Payload: session.SessionUpdatePayload{State: &session.StatePatch{ExecutionCWD: session.SetField(saved)}}}); err != nil {
		t.Fatal(err)
	}
	var rt Runtime
	defer resumeStop(t, &rt)
	params.CWD = initial
	if _, err := rt.Resume(params); err == nil {
		t.Fatal("initial cwd overrode saved execution cwd")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(saved, alias); err != nil {
		t.Fatal(err)
	}
	params.CWD = alias
	if _, err := rt.Resume(params); err != nil {
		t.Fatalf("canonical equivalent directory rejected: %v", err)
	}
}

func TestResumeChangedCataloguesAndRequest(t *testing.T) {
	owner, params, driver := resumeFixture(t)
	params.Skills = skill.NewRegistryForTest(map[string]*skill.Skill{"fixture": {Name: "fixture", State: skill.StateEnable}}, nil, nil)
	sections := params.System().Sections()
	var instruction session.NamedInstruction
	for _, section := range sections {
		if section.Render() != "" {
			instruction = session.NamedInstruction{Name: section.Name, InstructionSection: session.InstructionSection{Content: "obsolete text", Slot: int(section.Slot)}}
			break
		}
	}
	schemas := params.ToolSchemas()
	if len(schemas) == 0 || instruction.Name == "" {
		t.Fatal("missing native catalogues")
	}
	staleTool := schemas[0]
	staleTool.Description = "obsolete description"
	if err := session.NewRecorder(owner, "fake").RecordCatalogues(t.Context(), []session.NamedInstruction{instruction}, []ai.Schema{staleTool}, map[string]string{"fixture": session.SkillActive}, "boot"); err != nil {
		t.Fatal(err)
	}
	before := owner.Snapshot()
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	current := owner.Snapshot()
	changed := map[string]bool{}
	for _, event := range current.Events()[len(before.Events()):] {
		switch v := event.Payload.(type) {
		case *session.InstructionsUpdatePayload:
			changed["instruction"] = v.Cause == "resume" && v.Set[instruction.Name].Content != "obsolete text"
		case *session.ToolsetUpdatePayload:
			changed["tool"] = v.Cause == "resume" && v.Set[staleTool.Name].Description != "obsolete description"
		case *session.SkillsUpdatePayload:
			changed["skill"] = v.Cause == "resume" && v.Set["fixture"] == session.SkillEnabled
		}
	}
	if len(changed) != 3 || !changed["instruction"] || !changed["tool"] || !changed["skill"] {
		t.Fatalf("changed catalogues: %v", changed)
	}
	wantInstructions, wantTools := current.CatalogueDigests()
	if err := rt.Send(core.UserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-rt.Outbox():
			if _, ok := event.(core.TurnEnded); ok {
				resumeStop(t, &rt)
				p := owner.Snapshot()
				starts := 0
				for _, event := range p.Events() {
					if start, ok := event.Payload.(*session.InferenceStartPayload); ok {
						starts++
						if start.Input.Conversation == nil || start.InstructionsDigest != wantInstructions || start.ToolsetDigest != wantTools {
							t.Fatalf("incorrect current catalogue digests: %+v", start)
						}
						rebuilt, err := owner.InferenceRequest(t.Context(), event.Seq)
						if err != nil {
							t.Fatal(err)
						}
						actual := driver.Last()
						a, _ := json.Marshal(rebuilt)
						b, _ := json.Marshal(actual)
						if string(a) != string(b) {
							t.Fatalf("reconstructed request differs\n%s\n%s", a, b)
						}
					}
				}
				if starts != 1 {
					t.Fatalf("starts: %d", starts)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("no turn completion")
		}
	}
}

func TestResumeOutputStorageFailure(t *testing.T) {
	owner, params, _ := resumeFixture(t)
	driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		return aitest.Says("must not publish")(ctx, request)
	})
	params.Provider = runOnceProvider{driver}
	var rt Runtime
	defer resumeStop(t, &rt)
	if _, err := rt.Resume(params); err != nil {
		t.Fatal(err)
	}
	if err := rt.Send(core.UserMessage("go", nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-rt.Outbox():
			if end, ok := event.(sdkagent.TurnEnd); ok {
				if end.Err == nil {
					t.Fatal("missing terminal recording error")
				}
				resumeStop(t, &rt)
				if driver.Calls() != 1 {
					t.Fatalf("calls: %d", driver.Calls())
				}
				p := owner.Snapshot()
				for _, msg := range append(p.Messages(), rt.Messages()...) {
					if msg.Role == ai.RoleAssistant {
						t.Fatal("uncommitted output published")
					}
				}
				return
			}
		case <-ctx.Done():
			_ = rt.Stop(ctx)
			t.Fatal("no terminal event after recording failure")
		}
	}
}
