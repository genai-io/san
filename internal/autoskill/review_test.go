package autoskill

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func reviewRecording(t *testing.T) *session.Session {
	t.Helper()
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Create(t.Context(), session.CreateOptions{ID: "main", AgentID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	return owner
}

type reviewProvider struct{ driver *aitest.Driver }

func (p reviewProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}
func (reviewProvider) Name() string                                        { return "test" }
func (reviewProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }

func TestSideReviewOriginalSession(t *testing.T) {
	mgr, _ := newTestSkillManagerWithPerms(t, SkillPermissions{AllowCreate: true})
	owner := reviewRecording(t)
	driver := aitest.New(aitest.Replies(ai.Response{
		Content:    ai.TextContent(`{"action":"create","name":"go-table-tests","level":"project","description":"Patterns for Go tests","body":"Use named table cases.","note":"captured named test cases"}`),
		StopReason: ai.StopEndTurn, Usage: ai.Usage{Input: 17, Output: 4},
	}))
	evidence := []core.Message{core.UserMessage("unrelated private history", nil), core.UserMessage("Write the tests", nil), {Role: ai.RoleAssistant, Content: ai.TextContent("Named table cases made the failure easier to locate.")}}
	r := SideReview{Provider: reviewProvider{driver}, Model: "test", Recording: owner, Skills: mgr, Strategy: "Prefer concrete examples"}
	if _, err := r.Run(t.Context(), evidence); err != nil {
		t.Fatal(err)
	}
	call := driver.Last()
	if len(call.Tools) != 0 || len(call.Messages) != 1 {
		t.Fatalf("side card request carried tools or history: %+v", call)
	}
	text := call.Messages[0].Content.Text()
	if strings.Contains(text, "unrelated private history") || !strings.Contains(text, "Prefer concrete examples") || !strings.Contains(text, "Write the tests") {
		t.Fatalf("review evidence: %s", text)
	}
	got, err := mgr.Read("go-table-tests")
	if err != nil || !strings.Contains(got, "origin: agent-created") {
		t.Fatalf("skill=%q err=%v", got, err)
	}
	var started, finished, annotated bool
	snapshot := owner.Snapshot()
	for _, event := range snapshot.Events() {
		switch p := event.Payload.(type) {
		case *session.InferenceStartPayload:
			started = p.Purpose == session.PurposeAutoSkill
			if p.Input.Standalone == "" || p.Input.Conversation != nil {
				t.Fatalf("review input was not recorded as standalone: %+v", p.Input)
			}
		case *session.InferenceFinishPayload:
			finished = p.Usage.Input == 17 && p.Usage.Output == 4
		case *session.AnnotationAddPayload:
			annotated = strings.Contains(p.Text, "go-table-tests")
		case *session.MessageAppendPayload:
			t.Fatal("side card appended conversational input")
		}
	}
	if !started || !finished || !annotated {
		t.Fatalf("original recording: start=%v finish=%v annotation=%v", started, finished, annotated)
	}
	if err := owner.Err(); err != nil {
		t.Fatalf("review closed caller-owned writer: %v", err)
	}
}

func TestSideReviewDefaultStrategyAndToolResultExclusion(t *testing.T) {
	mgr, _ := newTestSkillManagerWithPerms(t, SkillPermissions{AllowCreate: true})
	driver := aitest.New(aitest.Says(`{"action":"none"}`))
	evidence := []core.Message{
		core.UserMessage("Write the tests", nil),
		core.AssistantMessage("Done", "", nil),
		core.ToolResultMessage(core.ToolResult{Content: ai.TextContent("private tool output")}),
	}
	r := SideReview{Provider: reviewProvider{driver}, Recording: reviewRecording(t), Skills: mgr}
	if _, err := r.Run(t.Context(), evidence); err != nil {
		t.Fatal(err)
	}
	call := driver.Last()
	if len(call.Messages) != 1 || call.Messages[0].Role != ai.RoleUser || len(call.Tools) != 0 {
		t.Fatalf("want one user message and no tools: %+v", call)
	}
	text := call.Messages[0].Content.Text()
	if !strings.Contains(text, "SKILLS — capture reusable") || !strings.Contains(text, "Write the tests") || strings.Contains(text, "private tool output") || strings.Contains(text, "memory_write") {
		t.Fatalf("unexpected review payload: %s", text)
	}
}

func TestSideReviewClosedOwnerDoesNotDispatch(t *testing.T) {
	owner := reviewRecording(t)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	mgr, _ := newTestSkillManagerWithPerms(t, SkillPermissions{AllowCreate: true})
	driver := aitest.New(aitest.Says(`{"action":"none"}`))
	_, err := (SideReview{Provider: reviewProvider{driver}, Recording: owner, Skills: mgr}).Run(t.Context(), []core.Message{core.UserMessage("Run the tests", nil)})
	if !errors.Is(err, session.ErrClosed) || driver.Calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, driver.Calls())
	}
}

func TestRejectUnauthorizedSkillProposals(t *testing.T) {
	perms := SkillPermissions{AllowCreate: true, AllowUpdate: true}
	used := map[string]string{"used-skill": "old"}
	for _, text := range []string{
		`{"action":"create","level":"user","description":"Test skill","body":"Use named cases.","note":"missing name"}`,
		`{"action":"delete","name":"used-skill","note":"old"}`,
		`{"action":"patch","name":"unrelated","old_text":"old","new_text":"new","note":"fix"}`,
		`{"action":"patch","name":"used-skill","old_text":"old","new_text":"new","note":"fix","replace_all":true}`,
		`{"action":"none"} {"action":"none"}`,
	} {
		if _, err := parseSkillProposal(text, perms, used); err == nil {
			t.Errorf("accepted %s", text)
		}
	}
}
