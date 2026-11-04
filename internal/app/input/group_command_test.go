package input

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/skill"
)

func TestGroupCommandInvokesHiddenSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := skill.Default()
	t.Cleanup(func() { skill.SetDefaultRegistry(old) })
	skill.Initialize(skill.Options{CWD: t.TempDir()})
	for _, args := range []string{"", "join shop", "leave", "hold on", "join the backend team"} {
		t.Run(args, func(t *testing.T) {
			in := &Model{}
			invoked := false
			c := NewSlashCommandController(SlashCommandEnv{
				Input: in, Skill: skill.Default(),
				HandleSkillInvocation: func() tea.Cmd { invoked = true; return nil },
				GroupCommand:          func(string) (string, tea.Cmd) { t.Fatal("membership bypassed skill"); return "", nil },
			})
			out, _, err := c.handleGroupCommand(context.Background(), args)
			if err != nil || out != "" || !invoked {
				t.Fatalf("out=%q err=%v invoked=%v", out, err, invoked)
			}
			if in.Skill.PendingFullName != "group" || !strings.Contains(in.Skill.PendingInstructions, "Group") {
				t.Fatal("missing group skill instructions")
			}
		})
	}
}

func TestGroupAdministrativeCommandsStayNative(t *testing.T) {
	for _, args := range []string{"members", "list", "kick api", "disband shop"} {
		called := false
		c := NewSlashCommandController(SlashCommandEnv{GroupCommand: func(got string) (string, tea.Cmd) {
			called = true
			if got != args {
				t.Fatalf("got %q want %q", got, args)
			}
			return "done", nil
		}})
		out, _, err := c.handleGroupCommand(context.Background(), args)
		if !called || out != "done" || err != nil {
			t.Fatalf("native command %q failed", args)
		}
	}
}
