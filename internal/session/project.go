package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
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
	Legacy                                     bool
}

type projectRegistration struct {
	Version    int    `json:"version"`
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Key        string `json:"lookup_key"`
	Anchor     string `json:"anchor"`
	ConfigRoot string `json:"config_root,omitempty"` // version 1 only; never a settings source
}

// Path reuse is detected when the kind or lookup key changes at the saved anchor.
// Other checkout reuse and replacing metadata at identical paths are not detected.

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
	projects := filepath.Join(confdir.Dir(home), "projects")
	// Keep the original spelling's exact-cwd partition readable before normalizing
	// symlinks. Never register it as the common repository or move its contents.
	for _, oldCwd := range []string{cwd, initial} {
		id := ProjectID(oldCwd)
		root := filepath.Join(projects, id)
		entries, err := os.ReadDir(filepath.Join(root, "transcripts"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return p, fmt.Errorf("inspect legacy partition: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
				p.ID, p.Root, p.Legacy = id, root, true
				return p, nil
			}
		}
	}
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
	want := projectRegistration{Version: 2, Kind: kind, Key: key, Anchor: p.Workspace}
	// WithLock waits in the OS; cancellation is checked on either side, not
	// during lock acquisition. Atomic publication does not promise power-loss sync.
	if err := ctx.Err(); err != nil {
		return p, err
	}
	err = atomicfile.WithLock(filepath.Join(projects, ".registry.lock"), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(projects)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(projects, entry.Name(), "project.json")
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			var saved projectRegistration
			if err := json.Unmarshal(data, &saved); err != nil {
				return fmt.Errorf("read registration %s: %w", path, err)
			}
			// Early version-1 registrations used the main checkout as an anchor.
			// Preserve their IDs without requiring that checkout to remain available.
			if saved.Version == 1 {
				saved.Anchor = saved.ConfigRoot
			}
			if (saved.Version != 1 && saved.Version != 2) || saved.ID != entry.Name() || saved.Key == "" || saved.Anchor == "" || (saved.Kind != "git" && saved.Kind != "directory") {
				return fmt.Errorf("invalid or unsupported project registration %s", path)
			}
			if saved.Kind != kind && (saved.Key == initial || saved.Anchor == p.Workspace) {
				return fmt.Errorf("project kind changed; explicit reconciliation required")
			}
			if saved.Key != key {
				if saved.Anchor == want.Anchor {
					return fmt.Errorf("project registration %s changed; explicit reconciliation required", saved.ID)
				}
				continue
			}
			if saved.Kind != kind || (kind == "directory" && saved.Anchor != want.Anchor) {
				return fmt.Errorf("project registration %s changed; explicit reconciliation required", saved.ID)
			}
			if p.ID != "" {
				return fmt.Errorf("ambiguous project registrations for %s; explicit reconciliation required", key)
			}
			p.ID, p.Root = saved.ID, filepath.Join(projects, saved.ID)
		}
		if p.ID != "" {
			return nil
		}
		want.ID = rand.Text()
		p.ID, p.Root = want.ID, filepath.Join(projects, want.ID)
		// Mkdir is also an ID collision check; never overwrite an existing partition.
		if err := os.Mkdir(p.Root, 0o700); err != nil {
			return err
		}
		return atomicfile.WriteJSON(filepath.Join(p.Root, "project.json"), want, 0o600)
	})
	return p, err
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
	if p.Legacy {
		if canonical != p.InitialCwd {
			return "", fmt.Errorf("legacy partition requires its exact startup workspace")
		}
		return canonical, nil
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
