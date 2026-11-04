package input

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestGroupCommandStaysOutsideMainSkillFlow(t *testing.T) {
	for _, args := range []string{"", "join shop", "leave", "hold on", "members", "list", "kick api", "disband shop", "帮我加入 backend"} {
		t.Run(args, func(t *testing.T) {
			called := false
			c := NewSlashCommandController(SlashCommandEnv{
				HandleSkillInvocation: func() tea.Cmd { t.Fatal("group entered main skill flow"); return nil },
				GroupCommand: func(got string) (string, tea.Cmd) {
					called = true
					if got != args {
						t.Fatalf("got %q want %q", got, args)
					}
					return "done", nil
				},
			})
			out, _, err := c.handleGroupCommand(context.Background(), args)
			if !called || out != "done" || err != nil {
				t.Fatalf("command failed: %q, %v", out, err)
			}
			if shouldPreserveCommandInConversation("/group " + args) {
				t.Fatal("group request would enter main conversation")
			}
		})
	}
}
