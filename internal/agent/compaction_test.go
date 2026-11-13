package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func compactMessages() []core.Message {
	msgs := make([]core.Message, core.MinMessagesToCompact)
	for i := range msgs {
		msgs[i] = core.UserMessage("keep this request", nil)
	}
	return msgs
}

func TestCompactRequestHookAndSummary(t *testing.T) {
	driver := aitest.New(aitest.Says("  concise summary  "))
	engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
	calls := 0
	engine.AddSessionFunctionHook(hook.PreCompact, "", hook.FunctionHook{Callback: func(ctx context.Context, input hook.HookInput) (hook.HookOutput, error) {
		calls++
		if input.Trigger != "manual" || input.CustomInstructions != "focus" {
			t.Fatalf("hook input: %+v", input)
		}
		if driver.Calls() != 0 {
			t.Fatal("summary ran before hook")
		}
		return hook.HookOutput{HookSpecificOutput: &hook.HookSpecificOutput{AdditionalContext: "hook guidance"}}, nil
	}})
	got := (CompactRequest{Ctx: t.Context(), Client: llm.NewClient(runOnceProvider{driver}, "fake", 0), Messages: compactMessages(), SummaryFocus: "focus", PreCompact: engine.Prepare(hook.PreCompact, hook.HookInput{Trigger: "manual", CustomInstructions: "focus"}), Trigger: "manual"}).Run()
	if got.Err != nil || !strings.HasPrefix(got.Summary, "concise summary") || got.OriginalCount != core.MinMessagesToCompact || got.Trigger != "manual" || calls != 1 {
		t.Fatalf("result: %+v, hooks=%d", got, calls)
	}
	request := driver.Last()
	if len(request.Messages) != 1 || !strings.Contains(request.Messages[0].Text(), "Focus the summary on: focus\nhook guidance") || !strings.Contains(request.Messages[0].Text(), "keep this request") {
		t.Fatalf("request: %+v", request)
	}
}

func TestCompactRequestHookFailureIsAdvisory(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "block", true: "error"}[fail], func(t *testing.T) {
			driver := aitest.New(aitest.Says("summary"))
			engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
			engine.AddSessionFunctionHook(hook.PreCompact, "", hook.FunctionHook{Callback: func(context.Context, hook.HookInput) (hook.HookOutput, error) {
				if fail {
					return hook.HookOutput{}, errors.New("hook failed")
				}
				allow := false
				return hook.HookOutput{Continue: &allow, StopReason: "advisory"}, nil
			}})
			result := (CompactRequest{Ctx: t.Context(), Client: llm.NewClient(runOnceProvider{driver}, "fake", 0), Messages: compactMessages(), PreCompact: engine.Prepare(hook.PreCompact, hook.HookInput{Trigger: "manual"}), Trigger: "manual"}).Run()
			if result.Err != nil || driver.Calls() != 1 {
				t.Fatalf("result=%+v, calls=%d", result, driver.Calls())
			}
		})
	}
}

func TestCompactRequestCapturesHookAndStopsOnRecordingFailure(t *testing.T) {
	driver := aitest.New(aitest.Says("must not dispatch"))
	engine := hook.NewEngine(setting.NewData(), "original", t.TempDir(), "")
	defer engine.Shutdown()
	engine.AddSessionFunctionHook(hook.PreCompact, "", hook.FunctionHook{Callback: func(_ context.Context, input hook.HookInput) (hook.HookOutput, error) {
		if input.SessionID != "original" {
			t.Error("queued hook retargeted replacement session")
		}
		return hook.HookOutput{}, nil
	}})
	boom := errors.New("hook recording failed")
	original, replacement := 0, 0
	engine.SetCompletionRecorder(func(context.Context, hook.HookFiredAudit) error { original++; return boom })
	req := CompactRequest{Ctx: t.Context(), Client: llm.NewClient(runOnceProvider{driver}, "fake", 0), Messages: compactMessages(), PreCompact: engine.Prepare(hook.PreCompact, hook.HookInput{Trigger: "manual"})}
	engine.SetSession("replacement", "")
	engine.SetCompletionRecorder(func(context.Context, hook.HookFiredAudit) error { replacement++; return nil })
	result := req.Run()
	if !errors.Is(result.Err, boom) || driver.Calls() != 0 || original != 1 || replacement != 0 {
		t.Fatalf("result=%+v model calls=%d original=%d replacement=%d", result, driver.Calls(), original, replacement)
	}
}

func TestCompactRequestFailures(t *testing.T) {
	for _, mode := range []string{"short", "empty", "provider", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			boom := errors.New("summary unavailable")
			response := ai.Response{Content: ai.TextContent("  "), StopReason: ai.StopEndTurn}
			if mode == "provider" {
				response.Err = boom
			}
			driver := aitest.New(aitest.Replies(response))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			msgs := compactMessages()
			if mode == "short" {
				msgs = nil
			}
			if mode == "cancelled" {
				cancel()
			}
			result := (CompactRequest{Ctx: ctx, Client: llm.NewClient(runOnceProvider{driver}, "fake", 0), Messages: msgs}).Run()
			if result.Err == nil || result.Summary != "" {
				t.Fatalf("result: %+v", result)
			}
			switch mode {
			case "short":
				if !errors.Is(result.Err, ErrNothingToCompact) || driver.Calls() != 0 {
					t.Fatalf("short: %+v, calls=%d", result, driver.Calls())
				}
			case "provider":
				if !errors.Is(result.Err, boom) {
					t.Fatal(result.Err)
				}
			case "cancelled":
				if !errors.Is(result.Err, context.Canceled) {
					t.Fatal(result.Err)
				}
			}
		})
	}
}

type compactHooks struct {
	hook.Handler
	scheduled func(hook.EventType, hook.HookInput)
}

func (h compactHooks) ExecuteAsync(event hook.EventType, input hook.HookInput) {
	h.scheduled(event, input)
}

func TestPostCompactReseedsContext(t *testing.T) {
	for _, trigger := range []string{"manual", "auto", ""} {
		t.Run(trigger, func(t *testing.T) {
			r := reminder.New()
			memory := "old memory"
			r.Register("memory", func() string { return memory })
			r.PrepareInput("first")
			r.Enqueue("obsolete notice")
			files := filecache.New()
			path := filepath.Join(t.TempDir(), "recent.go")
			if err := os.WriteFile(path, []byte("recent contents"), 0o600); err != nil {
				t.Fatal(err)
			}
			files.Touch(path)
			var order []string
			hooks := compactHooks{scheduled: func(event hook.EventType, input hook.HookInput) {
				if event != hook.PostCompact || input.Trigger == "" {
					t.Fatalf("hook: %s %+v", event, input)
				}
				order = append(order, "hook")
			}}
			count := PostCompact(trigger, hooks, r, files, func() {
				order = append(order, "memory")
				if !r.Prepared() {
					t.Fatal("reset before memory refresh")
				}
				memory = "fresh memory"
			})
			if !reflect.DeepEqual(order, []string{"hook", "memory"}) || r.Prepared() {
				t.Fatalf("order=%v, prepared=%v", order, r.Prepared())
			}
			next := r.PrepareInput("next")
			if !strings.Contains(next, "fresh memory") || strings.Contains(next, "obsolete notice") {
				t.Fatalf("next: %s", next)
			}
			if (count == 1) != (trigger == "manual") || strings.Contains(next, "recent contents") != (trigger == "manual") {
				t.Fatalf("restored=%d, next=%s", count, next)
			}
			if again := r.PrepareInput("again"); again != "again" {
				t.Fatalf("repeated: %s", again)
			}
		})
	}
}

func TestRecordManualCompactionReplayAndFailure(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "closed"}[closed], func(t *testing.T) {
			fixture := mainSessionFixture(t)
			store, opened := fixture.Store, fixture.Session
			messages := compactMessages()
			for i := range messages {
				messages[i].ID = core.NewMessageID()
			}
			if err := opened.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: messages}}); err != nil {
				t.Fatal(err)
			}
			driver := aitest.New(aitest.Says("summary"))
			completion := opened.Completion(session.PurposeCompaction)
			result := (CompactRequest{Ctx: t.Context(), Client: llm.NewClient(runOnceProvider{driver}, "fake", 0), Messages: messages, Completion: completion}).Run()
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if closed {
				if err := opened.Close(); err != nil {
					t.Fatal(err)
				}
			}
			summary, info, err := RecordManualCompaction(opened, result)
			if closed {
				if !errors.Is(err, session.ErrClosed) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || summary.ID == "" || info.SummaryMessageID != summary.ID || info.Trigger != "manual" {
				t.Fatalf("info=%+v err=%v", info, err)
			}
			if err := opened.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), opened.ID())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			history := reopened.Snapshot()
			active := history.ActiveMessages()
			if len(active) != 1 || active[0].ID != summary.ID || active[0].Text() != core.FormatCompactSummary(result.Summary) {
				t.Fatalf("active=%+v", active)
			}
			if len(history.Messages()) != len(messages)+1 {
				t.Fatal("compaction discarded full history")
			}
		})
	}
}
