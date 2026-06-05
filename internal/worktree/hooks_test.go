package worktree

import (
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
)

func TestWorktreeHooksFire(t *testing.T) {
	hook.SetDefaultEngine(hook.NewEngine(&setting.Data{}, "test", t.TempDir(), ""))
	defer hook.ResetDefaultEngine()

	repo := makeRepo(t)

	result, _, err := Create(repo, "hook-test")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	if err := Remove(repo, result.Path); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}
}
