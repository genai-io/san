package worktree

import "github.com/genai-io/san/v2/internal/hook"

func fireWorktreeCreated(name, path string) {
	if h := hook.DefaultEngine(); h != nil {
		h.ExecuteAsync(hook.WorktreeCreate, hook.HookInput{
			Name:         name,
			WorktreePath: path,
		})
	}
}

func fireWorktreeRemoved(path string) {
	if h := hook.DefaultEngine(); h != nil {
		h.ExecuteAsync(hook.WorktreeRemove, hook.HookInput{
			WorktreePath: path,
		})
	}
}
