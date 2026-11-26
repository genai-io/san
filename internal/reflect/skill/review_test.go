package skill

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
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
		t.Fatalf("Aside request carried tools or history: %+v", call)
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
			t.Fatal("Aside appended conversational input")
		}
	}
	if !started || !finished || !annotated {
		t.Fatalf("original recording: start=%v finish=%v annotation=%v", started, finished, annotated)
	}
	if err := owner.Err(); err != nil {
		t.Fatalf("review closed caller-owned writer: %v", err)
	}
}

func TestSideReviewRequiresUserEvidence(t *testing.T) {
	mgr, _ := newTestSkillManagerWithPerms(t, SkillPermissions{AllowCreate: true})
	driver := aitest.New(aitest.Says(`{"action":"none"}`))
	r := SideReview{Provider: reviewProvider{driver}, Model: "test", Recording: reviewRecording(t), Skills: mgr}
	evidence := []core.Message{core.UserMessage("<system-reminder>scheduled task</system-reminder>", nil), {Role: ai.RoleAssistant, Content: ai.TextContent("Assistant-only claim.")}}
	if notice, err := r.Run(t.Context(), evidence); notice != "" || err != nil || driver.Calls() != 0 {
		t.Fatalf("reminder-only turn reviewed: notice=%q err=%v calls=%d", notice, err, driver.Calls())
	}
}

func TestSideReviewDeletesOversizedUsedSkill(t *testing.T) {
	const name = "oversized-skill"
	// The multibyte rune straddles the prompt budget; the tail is never sent.
	prefix := strings.Repeat("a", 8*1024-1)
	for _, changed := range []bool{false, true} {
		label := "unchanged"
		if changed {
			label = "tail changed during review"
		}
		t.Run(label, func(t *testing.T) {
			mgr, cwd := newTestSkillManagerWithPerms(t, SkillPermissions{AllowDelete: true})
			creator := NewSkillManager(cwd, SkillPermissions{AllowCreate: true})
			if _, err := creator.Create(name, "Old test procedure", prefix+"é\nold tail", "project"); err != nil {
				t.Fatal(err)
			}
			p, err := mgr.requireAgentOwned(name)
			if err != nil {
				t.Fatal(err)
			}
			reply := aitest.Says(`{"action":"delete","name":"oversized-skill","note":"procedure is obsolete"}`)
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				if changed {
					body := strings.Replace(p.body, "old tail", "new tail", 1)
					if err := os.WriteFile(p.path, []byte(joinFrontmatter(p.fm, body)), 0o644); err != nil {
						return aitest.Fails(err)(ctx, req)
					}
				}
				return reply(ctx, req)
			})
			evidence := []core.Message{
				core.UserMessage("Follow the current test procedure", nil),
				core.AssistantMessage("", "", []core.ToolCall{{ID: "used", Name: "Skill", Input: `{"skill":"oversized-skill"}`}}),
				core.ToolResultMessage(core.ToolResult{ToolCallID: "used", Content: ai.TextContent("loaded")}),
			}
			r := SideReview{Provider: reviewProvider{driver}, Recording: reviewRecording(t), Skills: mgr}
			summary, err := r.Run(t.Context(), evidence)
			if driver.Calls() != 1 {
				t.Fatalf("review calls = %d, want 1 (err = %v)", driver.Calls(), err)
			}
			var payload struct {
				Used map[string]string `json:"editable_used_skills"`
			}
			if err := json.Unmarshal([]byte(driver.Last().Messages[0].Content.Text()), &payload); err != nil {
				t.Fatal(err)
			}
			if got := payload.Used[name]; got != prefix {
				t.Fatalf("review did not receive the expected clipped prefix (got %d bytes)", len(got))
			}
			if changed {
				if err == nil || !strings.Contains(err.Error(), "changed during Auto Skill review") {
					t.Fatalf("want stale revision rejection, got summary=%q err=%v", summary, err)
				}
				if content, err := os.ReadFile(p.path); err != nil || !strings.Contains(string(content), "new tail") {
					t.Fatalf("edited skill was not preserved: %v", err)
				}
			} else {
				if err != nil || !strings.Contains(summary, "delete "+name) {
					t.Fatalf("want successful delete, got summary=%q err=%v", summary, err)
				}
				if _, err := os.Stat(p.path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("skill file should be gone, stat err=%v", err)
				}
			}
		})
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
