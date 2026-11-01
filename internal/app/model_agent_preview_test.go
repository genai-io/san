package app

import (
	"testing"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestStartupAgentPreviewPreference(t *testing.T) {
	cwd := t.TempDir()
	for _, n := range []*int{nil, new(-1), new(0), new(7), new(99)} {
		settings := setting.NewData()
		settings.AgentPreviewLines = n
		environment := env{SessionPermissions: setting.NewSessionPermissions()}
		applyStartupSettings(&environment, settings, cwd, true, hook.NewEngine(settings, "", cwd, ""))
		if environment.AgentPreviewLines != settings.AgentPreviewLineCount() || environment.BashPreviewLines != setting.DefaultBashPreviewLines {
			t.Fatalf("startup previews = agent %d, bash %d", environment.AgentPreviewLines, environment.BashPreviewLines)
		}
	}
}

func TestAgentPreviewSavedUpdatesLivePreference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := fixedComposerModel(80, 24)
	m.env.CWD = t.TempDir()
	m.env.BashPreviewLines = 7
	for _, n := range []int{5, 0} {
		if err := setting.SaveAgentPreviewLines(n); err != nil {
			t.Fatal(err)
		}
		m.Update(input.AgentPreviewSavedMsg{Lines: n})
		if m.env.AgentPreviewLines != n || m.services.Setting.Snapshot().AgentPreviewLineCount() != n {
			t.Fatalf("saved %d did not update live and reloaded preferences", n)
		}
		if m.env.BashPreviewLines != 7 {
			t.Fatal("Agent preference changed Bash preview")
		}
	}
}
