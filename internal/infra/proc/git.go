package proc

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// GitStatus is a local, read-only repository snapshot. Repository is a display
// label, never a raw remote URL (which may contain credentials).
type GitStatus struct {
	Branch     string
	Repository string
	Staged     int
	Modified   int
	Untracked  int
	Conflicts  int
	Ahead      int
	Behind     int
}

// ReadGitStatus reads the worktree containing cwd, including linked worktrees
// and unborn or detached HEADs. It never contacts a remote, updates the index,
// or runs fsmonitor hooks. The caller supplies a bounded context.
// Outside a worktree, or if Git is unavailable, it returns an error.
func ReadGitStatus(ctx context.Context, cwd string) (GitStatus, error) {
	out, err := gitOutput(ctx, cwd, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return GitStatus{}, err
	}
	status := parseGitStatus(string(out))

	config, err := gitOutput(ctx, cwd, "config", "--null", "--get-regexp", `^(remote\..*\.url|branch\..*\.remote)$`)
	if err == nil {
		status.Repository = gitRepositoryLabel(string(config), status.Branch)
	}
	if status.Repository == "" {
		root, err := gitOutput(ctx, cwd, "rev-parse", "--show-toplevel")
		if err == nil {
			status.Repository = gitDisplayText(filepath.Base(strings.TrimSpace(string(root))))
		}
	}
	return status, nil
}

func gitOutput(ctx context.Context, cwd string, args ...string) ([]byte, error) {
	args = append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	// Clean filters may start children that keep output pipes open after Git exits.
	DetachSession(cmd)
	cmd.Cancel = func() error { return TerminateGroup(cmd, syscall.SIGKILL) }
	cmd.WaitDelay = 100 * time.Millisecond
	// An inherited Git command environment must not redirect this read to a
	// different repository or index than the UI's current working directory.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR":
			return true
		}
		return false
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := StartGroup(cmd); err != nil {
		return nil, err
	}
	err := cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitErr.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

func parseGitStatus(output string) GitStatus {
	var status GitStatus
	var oid string
	records := strings.Split(output, "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			status.Branch = gitDisplayText(strings.TrimPrefix(record, "# branch.head "))
		case strings.HasPrefix(record, "# branch.oid "):
			oid = strings.TrimPrefix(record, "# branch.oid ")
		case strings.HasPrefix(record, "# branch.ab "):
			fields := strings.Fields(strings.TrimPrefix(record, "# branch.ab "))
			if len(fields) == 2 {
				status.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[0], "+"))
				status.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[1], "-"))
			}
		case strings.HasPrefix(record, "1 "), strings.HasPrefix(record, "2 "):
			if len(record) >= 4 {
				if record[2] != '.' {
					status.Staged++
				}
				if record[3] != '.' {
					status.Modified++
				}
			}
			if record[0] == '2' {
				// A rename/copy has a second NUL-delimited original path. It is
				// not a status record, even if the filename begins with "? ".
				i++
			}
		case strings.HasPrefix(record, "? "):
			status.Untracked++
		case strings.HasPrefix(record, "u "):
			status.Conflicts++
		}
	}
	if status.Branch == "(detached)" && oid != "" && oid != "(initial)" {
		status.Branch = oid[:min(7, len(oid))] + " (detached)"
	}
	return status
}

func gitRepositoryLabel(config, branch string) string {
	remotes := make(map[string]string)
	trackedRemote := ""
	for record := range strings.SplitSeq(config, "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if !ok {
			continue
		}
		if key == "branch."+branch+".remote" {
			trackedRemote = value
		}
		if name, ok := strings.CutPrefix(key, "remote."); ok {
			if name, ok = strings.CutSuffix(name, ".url"); ok && remotes[name] == "" {
				remotes[name] = value
			}
		}
	}
	if label := gitRemoteLabel(remotes[trackedRemote]); label != "" {
		return label
	}
	if label := gitRemoteLabel(remotes["origin"]); label != "" {
		return label
	}
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if label := gitRemoteLabel(remotes[name]); label != "" {
			return label
		}
	}
	return ""
}

func gitRemoteLabel(remote string) string {
	if remote == "" || (!strings.Contains(remote, "://") && strings.Contains(remote, "::")) {
		// Remote-helper syntax may contain commands or credentials, not a
		// repository URL. Never render that opaque value.
		return ""
	}
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		if u.Scheme == "file" {
			return gitDisplayText(strings.TrimSuffix(filepath.Base(u.Path), ".git"))
		}
		if u.Hostname() != "" {
			// Deliberately omit userinfo, port, query and fragment.
			return gitDisplayText(u.Hostname() + "/" + strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"))
		}
		return ""
	}
	// SCP-style SSH remotes, e.g. git@github.com:owner/repo.git.
	if userHost, path, ok := strings.Cut(remote, ":"); ok && !strings.ContainsAny(userHost, `/\\`) && len(userHost) > 1 {
		_, host, hasUser := strings.Cut(userHost, "@")
		if !hasUser {
			host = userHost
		}
		return gitDisplayText(host + "/" + strings.TrimSuffix(strings.Trim(path, "/"), ".git"))
	}
	// Local remotes can contain private directory names; show only the basename.
	return gitDisplayText(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(remote, `\`, "/")), ".git"))
}

func gitDisplayText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}
