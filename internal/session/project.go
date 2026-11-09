package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
)

// projectIdentity is a storage association, never a trust decision.
type projectIdentity struct {
	ID, Root, Workspace, InitialCwd, Key, Kind string
}

// projectFile is project.json: a description for people and tools. Identity is
// derived from the key, never read back from this file.
type projectFile struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Name    string `json:"name"`
}

// deriveProjectID names a project by what it is: its kind and canonical key (the
// Git common directory, or the exact directory outside Git). The same place
// always maps to the same ID, with no registry to lock, scan or reconcile; a
// repository that moves is a new project.
func deriveProjectID(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + key))
	return hex.EncodeToString(sum[:12])
}

func resolveProject(ctx context.Context, home, cwd string) (projectIdentity, error) {
	var p projectIdentity
	if err := ctx.Err(); err != nil {
		return p, err
	}
	initial, err := canonicalProjectPath(cwd)
	if err != nil {
		return p, err
	}
	p.InitialCwd, p.Workspace = initial, initial
	kind, key := "directory", initial
	common, gitErr := projectGit(ctx, initial, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitErr == nil {
		kind = "git"
		key, err = canonicalProjectPath(strings.TrimSuffix(common, "\n"))
		if err != nil {
			return p, fmt.Errorf("resolve git common directory: %w", err)
		}
		top, err := projectGit(ctx, initial, "rev-parse", "--show-toplevel")
		if err != nil {
			return p, err
		}
		p.Workspace, err = canonicalProjectPath(strings.TrimSuffix(top, "\n"))
		if err != nil {
			return p, err
		}
	} else {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if !projectIsNonGit(initial, gitErr) {
			return p, gitErr
		}
	}
	p.Key, p.Kind = key, kind
	p.ID = deriveProjectID(kind, key)
	p.Root = filepath.Join(confdir.Dir(home), "projects", p.ID)
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return p, err
	}
	// Every writer publishes the same content, so racing writers need no lock.
	name := filepath.Base(p.Workspace)
	if kind == "git" {
		name = filepath.Base(filepath.Dir(key))
		if filepath.Base(key) != ".git" {
			name = filepath.Base(key)
		}
	}
	return p, atomicfile.WriteJSON(filepath.Join(p.Root, "project.json"), projectFile{Version: 1, Kind: kind, Key: key, Name: name}, 0o600)
}

// validateProjectWorkspace validates an explicit workspace without registering it.
// The returned path is canonical; trust remains the caller's responsibility.
func validateProjectWorkspace(ctx context.Context, p projectIdentity, workspace string) (string, error) {
	canonical, err := canonicalProjectPath(workspace)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key, kind := canonical, "directory"
	common, gitErr := projectGit(ctx, canonical, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitErr == nil {
		kind = "git"
		key, err = canonicalProjectPath(strings.TrimSuffix(common, "\n"))
		if err != nil {
			return "", err
		}
		top, err := projectGit(ctx, canonical, "rev-parse", "--show-toplevel")
		if err != nil {
			return "", err
		}
		canonical, err = canonicalProjectPath(strings.TrimSuffix(top, "\n"))
		if err != nil {
			return "", err
		}
	} else if !projectIsNonGit(canonical, gitErr) {
		return "", gitErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key != p.Key || kind != p.Kind {
		return "", fmt.Errorf("workspace belongs to a different project")
	}
	return canonical, nil
}

func canonicalProjectPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %s", resolved)
	}
	return resolved, nil
}

// Missing Git is harmless only when there is no repository marker. This scan
// is solely a failure guard, never nearest-ancestor non-Git project discovery.
func projectIsNonGit(cwd string, gitErr error) bool {
	if !errors.Is(gitErr, exec.ErrNotFound) && !strings.Contains(gitErr.Error(), "not a git repository") {
		return false
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); !errors.Is(err, os.ErrNotExist) {
			return false
		}
		if dir == filepath.Dir(dir) {
			return true
		}
	}
}

func projectGit(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...)
	// Startup location, not ambient Git command overrides, determines ownership.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") && !strings.HasPrefix(env, "LC_ALL=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve project git: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
