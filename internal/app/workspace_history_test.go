package app

import (
	"slices"
	"testing"

	"github.com/genai-io/san/internal/app/kit/history"
)

func TestWorkspaceChangeKeepsSubmissionHistoryLocal(t *testing.T) {
	home, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	history.Save(a, []string{"sent A"})
	history.Save(b, []string{"sent B"})
	m := fixedComposerModel(80, 24)
	m.env.CWD = a
	m.userInput.History.Items = history.Load(a)
	m.learnedStores = newLearnedStoreContext(a, m.services.Setting)
	m.userInput.Textarea.SetValue("/help")
	m.changeCwd(b)
	m.userInput.HistoryUp()
	if got := m.userInput.Textarea.Value(); got != "sent B" {
		t.Errorf("B recall = %q", got)
	}
	m.userInput.HistoryDown()
	if got := m.userInput.Textarea.Value(); got != "/help" {
		t.Fatalf("typed draft = %q", got)
	}
	m.handleSubmit()
	if got := history.Load(b); !slices.Equal(got, []string{"sent B", "/help"}) {
		t.Errorf("B history after submission = %q", got)
	}
	if got := history.Load(a); !slices.Equal(got, []string{"sent A"}) {
		t.Errorf("A history after submission = %q", got)
	}
	m.userInput.Textarea.SetValue("next draft")
	m.userInput.HistoryUp()
	m.changeCwd(a)
	if got := m.userInput.Textarea.Value(); got != "next draft" {
		t.Errorf("draft behind recalled entry = %q", got)
	}
	m.userInput.HistoryUp()
	if got := m.userInput.Textarea.Value(); got != "sent A" {
		t.Errorf("A recall = %q", got)
	}
}
