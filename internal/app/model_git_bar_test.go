package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/setting"
)

func TestGitBarStartupPreference(t *testing.T) {
	for _, on := range []bool{false, true} {
		cwd := t.TempDir()
		settings := setting.NewData()
		settings.GitBar = &on
		environment := env{SessionPermissions: setting.NewSessionPermissions()}
		applyStartupSettings(&environment, settings, cwd, true, hook.NewEngine(settings, "", cwd, ""))
		if environment.ShowGitBar != on {
			t.Fatalf("startup ShowGitBar = %v, want %v", environment.ShowGitBar, on)
		}
	}
}

func TestGitBarEnableDisableAndStaleResults(t *testing.T) {
	m := &model{env: env{CWD: t.TempDir()}}
	if cmd := m.syncGitBar(); cmd != nil || m.gitBar.cwd != "" {
		t.Fatal("disabled Git bar started polling")
	}
	m.env.ShowGitBar = true
	cmd := m.syncGitBar()
	if cmd == nil || m.gitBar.cwd != m.env.CWD || m.gitBar.cancel == nil {
		t.Fatal("enabling must immediately start an asynchronous read")
	}
	defer m.cancelGitBarRead()
	if m.syncGitBar() != nil || m.refreshGitBar() != nil {
		t.Fatal("ordinary updates must not duplicate an in-flight read")
	}
	generation := m.gitBar.generation
	msg := gitStatusMsg{generation: generation, cwd: m.env.CWD, status: proc.GitStatus{Branch: "main"}}
	if cmd := m.handleGitStatus(msg); cmd == nil || m.gitBar.cancel != nil || m.gitBar.status.Branch != "main" {
		t.Fatal("valid result must update the snapshot and schedule a poll")
	}
	if cmd := m.handleGitBarTick(gitBarTickMsg{generation: generation}); cmd == nil || m.gitBar.cancel == nil {
		t.Fatal("valid poll tick did not start a read")
	}
	m.env.ShowGitBar = false
	if cmd := m.syncGitBar(); cmd != nil || m.gitBar.cwd != "" || m.gitBar.cancel != nil {
		t.Fatal("disabling must cancel and stop polling")
	}
	if cmd := m.handleGitStatus(msg); cmd != nil || m.gitBar.status.Branch != "" {
		t.Fatal("stale result restored the bar after disabling")
	}
	if cmd := m.handleGitBarTick(gitBarTickMsg{generation: generation}); cmd != nil {
		t.Fatal("stale tick restarted polling after disabling")
	}
	m.env.ShowGitBar = true
	if cmd := m.syncGitBar(); cmd == nil {
		t.Fatal("re-enabling did not restart polling")
	}
	if cmd := m.handleGitStatus(msg); cmd != nil || m.gitBar.status.Branch != "" {
		t.Fatal("an earlier generation was accepted after re-enabling")
	}
}

func TestGitBarWorkspaceChangeCancelsAndClears(t *testing.T) {
	m := &model{env: env{CWD: t.TempDir(), ShowGitBar: true}}
	cmd := m.syncGitBar()
	if cmd == nil {
		t.Fatal("expected initial read")
	}
	oldGeneration, oldCwd := m.gitBar.generation, m.env.CWD
	m.gitBar.status = proc.GitStatus{Branch: "old"}
	cancelled := false
	originalCancel := m.gitBar.cancel
	m.gitBar.cancel = func() { cancelled = true; originalCancel() }
	m.env.CWD = t.TempDir()
	if got := m.renderGitBar(); got != "" {
		t.Fatal("old repository displayed in a different workspace")
	}
	if cmd := m.syncGitBar(); cmd == nil || !cancelled || m.gitBar.status.Branch != "" {
		t.Fatal("workspace change must cancel the old read and clear the snapshot")
	}
	defer m.cancelGitBarRead()
	if cmd := m.handleGitStatus(gitStatusMsg{generation: oldGeneration, cwd: oldCwd, status: proc.GitStatus{Branch: "old"}}); cmd != nil {
		t.Fatal("old-workspace result restarted polling")
	}
	if cmd := m.handleGitBarTick(gitBarTickMsg{generation: oldGeneration}); cmd != nil {
		t.Fatal("old-workspace tick restarted polling")
	}
}

func TestGitBarSettingsFollowWorkspaceChange(t *testing.T) {
	home, oldCwd, newCwd := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestFile(t, filepath.Join(oldCwd, ".san", "settings.json"), `{"gitBar":true}`)
	writeTestFile(t, filepath.Join(newCwd, ".san", "settings.json"), `{"gitBar":false}`)
	settings := setting.New(setting.NewData())
	if err := settings.Reload(oldCwd); err != nil {
		t.Fatal(err)
	}
	m := fixedComposerModel(80, 24)
	m.env.CWD, m.env.ShowGitBar = oldCwd, true
	m.services.Setting = settings
	m.learnedStores = newLearnedStoreContext(oldCwd, settings)
	m.userInput.Settings = input.NewSettingsSelector(settings)
	m.userInput.Settings.Enter(80, 24)
	m.changeCwd(newCwd)
	if m.env.ShowGitBar {
		t.Fatal("new workspace did not disable the Git bar")
	}
	if !m.userInput.Settings.IsActive() {
		t.Fatal("workspace change closed the active Settings popup")
	}
	m.userInput.Settings.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.userInput.Settings.Enter(80, 24)
	m.userInput.Settings.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	m.userInput.Settings.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	panel := m.userInput.Settings.ActivePanel()
	if panel.Dirty() || !strings.Contains(ansi.Strip(panel.Render(70, 8)), "▸ Git information") {
		t.Fatal("Git toggle was not focused at its saved value")
	}
	m.userInput.Settings.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyRight})
	if !panel.Dirty() {
		t.Fatal("Git toggle still uses the old workspace's On baseline")
	}
}

func TestGitBarReadFailureHidesSnapshot(t *testing.T) {
	m := &model{env: env{CWD: t.TempDir(), ShowGitBar: true}}
	cmd := m.syncGitBar()
	defer m.cancelGitBarRead()
	msg, ok := cmd().(gitStatusMsg)
	if !ok || msg.status != (proc.GitStatus{}) {
		t.Fatalf("non-repository read = %#v", msg)
	}
	if m.refreshGitBar() != nil {
		t.Fatal("completed read must block another read until its result is handled")
	}
	m.gitBar.status = proc.GitStatus{Branch: "stale"}
	m.handleGitStatus(msg)
	if got := m.renderGitBar(); got != "" {
		t.Fatalf("read failure retained stale status: %q", got)
	}
}

func TestGitBarFooterFitsAndKeepsComposerCursor(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.env.CWD = t.TempDir()
	m.userInput.Textarea.SetValue("composer")
	m.userInput.Textarea.CursorEnd()
	m.gitBar.cwd = m.env.CWD
	m.gitBar.status = proc.GitStatus{Branch: "feature/topic", Repository: "github.com/owner/repo", Modified: 1}
	_, disabledCursor := m.viewString()
	m.env.ShowGitBar = true
	frame, cursor := m.viewString()
	if rows := rowCount(frame); rows != m.env.Height {
		t.Fatalf("frame has %d rows, want %d", rows, m.env.Height)
	}
	if cursor == nil || cursor.Position.Y != disabledCursor.Position.Y-1 {
		t.Fatalf("extra footer row did not adjust composer cursor: off=%#v, on=%#v", disabledCursor, cursor)
	}
	if !strings.Contains(ansi.Strip(frame), "feature/topic") {
		t.Fatalf("Git bar absent from footer: %q", ansi.Strip(frame))
	}
	m.env.ShowGitBar = false
	frame, _ = m.viewString()
	if strings.Contains(ansi.Strip(frame), "feature/topic") {
		t.Fatal("disabled Git bar still rendered")
	}
}

func TestGitBarSavedAppliesEffectivePreference(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := fixedComposerModel(80, 24)
	m.env.CWD = cwd
	for _, on := range []bool{true, false} {
		if err := setting.SaveGitBar(on); err != nil {
			t.Fatal(err)
		}
		m.Update(input.GitBarSavedMsg{On: on})
		if (m.gitBar.cwd != "") != on || (m.gitBar.cancel != nil) != on {
			t.Fatalf("saved on=%v did not start/stop polling", on)
		}
		if m.env.ShowGitBar != on || m.services.Setting.Snapshot().ShowGitBar() != on {
			t.Fatalf("saved on=%v did not update live and reloaded preferences", on)
		}
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.json"), []byte(`{"gitBar":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setting.SaveGitBar(true); err != nil {
		t.Fatal(err)
	}
	m.Update(input.GitBarSavedMsg{On: true})
	if m.env.ShowGitBar || m.gitBar.cwd != "" {
		t.Fatal("user preference incorrectly overrode a project-level false")
	}
}

func TestGitBarDisableCancelsReadContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{gitBar: gitBarState{cwd: t.TempDir(), cancel: cancel}}
	m.syncGitBar()
	if ctx.Err() == nil {
		t.Fatal("disable did not cancel the active read")
	}
}
