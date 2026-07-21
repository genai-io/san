package app

import (
	"testing"
	"time"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/setting"
)

func modelWithPromptHook(t *testing.T, command string) *model {
	t.Helper()
	data := setting.NewData()
	data.Hooks = map[string][]setting.Hook{
		string(hook.UserPromptSubmit): {{
			Hooks: []setting.HookCmd{{
				Type:    "command",
				Command: command,
				// Far longer than the UI budget, and longer than a user would
				// ever want to stare at a frozen terminal.
				Timeout: 600,
			}},
		}},
	}
	return &model{
		services: services{
			Hook:     hook.NewEngine(data, "test-session", t.TempDir(), ""),
			Reminder: reminder.NewService(),
			Setting:  setting.New(data),
		},
	}
}

// A UserPromptSubmit hook is a gate between a keypress and the submit, and it
// runs on the bubbletea goroutine. The engine's 600s default is meant for
// detached hooks; here it froze the whole UI — no repaint, and Esc and Ctrl+C
// queued behind the loop that would have dispatched them.
func TestPromptHookCannotFreezeTheUI(t *testing.T) {
	m := modelWithPromptHook(t, "sleep 30")

	start := time.Now()
	blocked, _ := m.checkPromptHook(t.Context(), "hello")
	elapsed := time.Since(start)

	if elapsed > defaultUIHookBudget+2*time.Second {
		t.Errorf("the UI was blocked for %v; the budget is %v", elapsed, defaultUIHookBudget)
	}
	// Fail open: a hook that hung must not swallow the user's prompt.
	if blocked {
		t.Error("a hook cut short at the budget blocked the prompt")
	}
}

// The budget must not change the answer for a hook that responds in time.
func TestPromptHookStillBlocksWhenItSaysSo(t *testing.T) {
	m := modelWithPromptHook(t, "echo 'nope' >&2; exit 2")

	blocked, reason := m.checkPromptHook(t.Context(), "hello")

	if !blocked {
		t.Fatal("a hook exiting 2 should block the prompt")
	}
	if reason == "" {
		t.Error("the block reason was lost")
	}
}

// And a fast hook that permits the prompt still permits it.
func TestPromptHookAllowsAFastPermit(t *testing.T) {
	m := modelWithPromptHook(t, "exit 0")

	if blocked, _ := m.checkPromptHook(t.Context(), "hello"); blocked {
		t.Error("a successful hook blocked the prompt")
	}
}

// The budget honors the hookUITimeout setting so a legitimately slow gate can
// raise it, and falls back to the default for an empty, unparseable, or
// non-positive value.
func TestUIHookBudgetHonorsSetting(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", defaultUIHookBudget},
		{"20s", 20 * time.Second},
		{"1500ms", 1500 * time.Millisecond},
		{"garbage", defaultUIHookBudget},
		{"0s", defaultUIHookBudget},
		{"-3s", defaultUIHookBudget},
	}
	for _, tc := range cases {
		data := setting.NewData()
		data.HookUITimeout = tc.raw
		m := &model{services: services{Setting: setting.New(data)}}
		if got := m.uiHookBudget(); got != tc.want {
			t.Errorf("hookUITimeout=%q: budget=%v, want %v", tc.raw, got, tc.want)
		}
	}
}
