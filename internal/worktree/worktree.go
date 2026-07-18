// Package worktree provides git worktree creation and management.
// Used by the EnterWorktree tool to move the main conversation into a
// disposable git worktree.
package worktree

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/log"
)

const worktreeDir = "agent-worktrees"

// Result contains the outcome of creating a worktree.
type Result struct {
	Path   string // absolute path to the worktree
	Branch string // branch name (empty for detached HEAD)
}

// Create creates a git worktree under baseCwd/.git/agent-worktrees/<slug>.
// If slug is empty, a random short ID is used.
// Returns the worktree path and a cleanup function that removes it.
func Create(baseCwd, slug string) (*Result, func(), error) {
	if slug == "" {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("generate worktree slug: %w", err)
		}
		slug = hex.EncodeToString(b)
	}

	// Prevent path traversal via crafted slug
	if strings.ContainsAny(slug, "/\\") || strings.Contains(slug, "..") {
		return nil, nil, fmt.Errorf("invalid worktree slug: must not contain path separators or '..'")
	}

	worktreePath := filepath.Join(baseCwd, ".git", worktreeDir, slug)

	cmd := exec.Command("git", "worktree", "add", "--detach", worktreePath)
	cmd.Dir = baseCwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("git worktree add failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	cleanup := func() {
		if err := Remove(baseCwd, worktreePath); err != nil {
			log.Logger().Warn("worktree cleanup failed",
				zap.String("path", worktreePath),
				zap.Error(err))
		}
	}
	fireWorktreeCreated(slug, worktreePath)

	return &Result{Path: worktreePath}, cleanup, nil
}

// Remove force-removes a git worktree.
func Remove(baseCwd, worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = baseCwd
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	fireWorktreeRemoved(worktreePath)
	return nil
}

// HasUncommittedChanges returns true if the worktree has uncommitted modifications.
func HasUncommittedChanges(worktreePath string) bool {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return true // assume dirty on error
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// HasUnmergedCommits returns true if the worktree has commits not present in the main repo.
func HasUnmergedCommits(worktreePath string) bool {
	cmd := exec.Command("git", "log", "--oneline", "HEAD", "--not", "--remotes", "--max-count=1")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// OriginalPath returns the main repository path for a managed worktree path.
// The worktree must be under <repo>/.git/agent-worktrees/<slug>.
func OriginalPath(worktreePath string) (string, bool) {
	clean := filepath.Clean(worktreePath)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) != worktreeDir {
		return "", false
	}
	gitDir := filepath.Dir(parent)
	if filepath.Base(gitDir) != ".git" {
		return "", false
	}
	return filepath.Dir(gitDir), true
}
