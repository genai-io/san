package setting

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/genai-io/san/internal/atomicfile"
	"github.com/genai-io/san/internal/confdir"
)

// Workspace trust: a folder's own configuration (hooks, MCP servers, plugins,
// allow rules) applies only once trusted; untrusted, only deny/ask rules apply.
// Trust is keyed on the repository's main checkout, covering its worktrees.

// trustEnv set to 1 trusts the working directory for this run only, for CI
// and scripts that cannot answer the prompt.
const trustEnv = "TRUST_WORKSPACE"

var (
	trustMu sync.Mutex
	// sessionTrust holds keys trusted for this process only.
	sessionTrust = map[string]bool{}
)

type trustFile struct {
	Trusted map[string]string `json:"trusted"` // key → time trusted (RFC 3339)
}

// TrustKey is the folder whose trust covers cwd: the main checkout of its
// repository, or cwd itself outside one.
func TrustKey(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return confdir.MainRepoRoot(cwd)
}

// IsWorkspaceTrusted reports whether cwd's own configuration may apply. The
// home directory is always trusted: its project directory is the user's own.
func IsWorkspaceTrusted(cwd string) bool {
	if Getenv(trustEnv) == "1" {
		return true
	}
	key := TrustKey(cwd)
	if home, err := os.UserHomeDir(); err == nil && key == home {
		return true
	}
	trustMu.Lock()
	defer trustMu.Unlock()
	if sessionTrust[key] {
		return true
	}
	return readTrust().Trusted[key] != ""
}

// TrustWorkspace trusts cwd's folder from now on.
func TrustWorkspace(cwd string) error {
	key := TrustKey(cwd)
	trustMu.Lock()
	defer trustMu.Unlock()
	f := readTrust()
	if f.Trusted == nil {
		f.Trusted = map[string]string{}
	}
	f.Trusted[key] = time.Now().UTC().Format(time.RFC3339)
	invalidateLoaded() // the project's settings now apply
	return atomicfile.WriteJSON(trustPath(), f, 0o600)
}

// TrustWorkspaceForSession trusts cwd's folder until San exits. Used when the
// folder carries nothing to review, so what the user adds to it later in the
// session is their own.
func TrustWorkspaceForSession(cwd string) {
	trustMu.Lock()
	defer trustMu.Unlock()
	sessionTrust[TrustKey(cwd)] = true
	invalidateLoaded()
}

// UntrackedInRepo reports whether path sits in a git repository without being
// tracked there. A clone brings only tracked files, so an untracked local
// config file (settings.local.json, mcp.local.json) is the user's own and
// applies without trust.
func UntrackedInRepo(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root := confdir.RepoRoot(filepath.Dir(abs))
	if !IsGitRepo(root) {
		return false
	}
	err = exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", abs).Run()
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1 // 1: known to git as untracked
}

// ProjectSettingsFindings lists what cwd's settings files would turn on that
// waits for trust, one line per item, each naming its file. Untracked local
// files are the user's own and are left out.
func ProjectSettingsFindings(cwd string) []string {
	var out []string
	for _, src := range NewLoaderForCwd(cwd).projectSettingsFiles() {
		var d Data
		if atomicfile.ReadJSON(src.path, &d) != nil || src.local && UntrackedInRepo(src.path) {
			continue
		}
		file, _ := filepath.Rel(cwd, src.path)
		add := func(what string, items []string) {
			if len(items) > 0 {
				slices.Sort(items)
				out = append(out, what+": "+strings.Join(items, ", ")+"  ("+file+")")
			}
		}
		add("run hooks on", slices.Collect(maps.Keys(d.Hooks)))
		add("allow without asking", d.Permissions.Allow)
		if d.Permissions.DefaultMode != "" {
			add("start in mode", []string{d.Permissions.DefaultMode})
		}
		add("set environment", slices.Collect(maps.Keys(d.Env)))
		add("enable plugins", slices.Collect(maps.Keys(d.EnabledPlugins)))
	}
	return out
}

func trustPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(confdir.Dir(home), "trust.json")
}

func readTrust() trustFile {
	var f trustFile
	_ = atomicfile.ReadJSON(trustPath(), &f)
	return f
}
