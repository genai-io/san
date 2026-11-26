package tui

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/todo"
)

func TestAutoSkillNotice(t *testing.T) {
	const result = "Auto Skill updated go-testing."
	for _, tc := range []struct {
		name          string
		notice        string
		cancelBefore  bool
		cancelPending bool
		switchSession bool
		wantQueued    bool
		wantDisplay   bool
	}{
		{name: "accepted", notice: result, wantQueued: true, wantDisplay: true},
		{name: "empty result"},
		{name: "cancelled before enqueue", notice: result, cancelBefore: true},
		{name: "cancelled while pending", notice: result, cancelPending: true, wantQueued: true},
		{name: "different owner", notice: result, switchSession: true, wantQueued: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := new(session.Session)
			m := &model{
				services:    services{Session: owner, Subagent: subagent.NewRegistry(), Tracker: todo.NewStore()},
				mainNotices: make(chan mainNotice, 1),
				conv:        chat.NewModel(80),
			}
			m.conv.OnNotice = func(core.ChatMessage) { t.Error("already-recorded notice was saved again") }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			queueAutoSkillNotice(ctx, m.mainNotices, owner, tc.notice)
			if got := len(m.mainNotices); (got == 1) != tc.wantQueued {
				t.Fatalf("notice queue length = %d, want queued: %t", got, tc.wantQueued)
			}
			if !tc.wantQueued {
				return
			}
			notice := <-m.mainNotices
			if notice.Destination != owner || notice.Content != "" || notice.FromAgent {
				t.Fatalf("expected a display-only notice for the original owner: %+v", notice)
			}
			if tc.cancelPending {
				m.services.AutoSkill.session = &autoSkillSession{cancel: cancel}
				m.teardownAutoSkill()
			}
			if tc.switchSession {
				m.services.Session = new(session.Session)
			}
			m.dispatch(mainNoticeMsg{notice: notice})
			if !tc.wantDisplay {
				if len(m.conv.Messages) != 0 {
					t.Fatalf("stale notice reached the conversation: %+v", m.conv.Messages)
				}
				return
			}
			if got := m.conv.Messages; len(got) != 1 || got[0].Role != core.ChatNotice || got[0].Content != result {
				t.Fatalf("expected one plain notice, got %+v", got)
			}
		})
	}
}
