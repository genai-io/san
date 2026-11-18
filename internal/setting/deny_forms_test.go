package setting

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

// A deny rule on a directory holds however the path is spelled.
func TestPathDenyRuleHoldsForEverySpelling(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "secrets"), filepath.Join(dir, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &Data{}
	d.Permissions.Deny = []string{"Read(" + real + "/secrets/**)", "Edit(" + real + "/secrets/**)"}
	session := &SessionPermissions{Mode: ModeAutoAccept, WorkingDirectories: []string{dir}}
	for _, path := range []string{
		real + "/secrets/key",
		real + "/proj/../secrets/key",
		real + "//secrets/key",
		dir + "/link/key",
	} {
		for _, tool := range []string{"Read", "Edit"} {
			if got := d.HasPermissionToUseTool(tool, map[string]any{"file_path": path}, session); got.Behavior != perm.Reject {
				t.Errorf("%s(%s) = %v, want deny", tool, path, got)
			}
		}
	}
	if d.CanApproveByHook("Read", map[string]any{"file_path": real + "/x/../secrets/key"}, session) {
		t.Error("a hook may not approve a denied path")
	}
}

// An allow rule on a directory must not admit a path that climbs out of it.
func TestPathAllowRuleIgnoresDotDotEscape(t *testing.T) {
	d := &Data{}
	d.Permissions.Allow = []string{"Write(/repo/src/**)"}
	got := d.HasPermissionToUseTool("Write", map[string]any{"file_path": "/repo/src/../../home/u/.profile"}, &SessionPermissions{Mode: ModeNormal})
	if got.Behavior == perm.Permit {
		t.Fatalf("allow rule admitted an escaping path: %v", got)
	}
}

// Wrappers, a leading backslash and "sh -c" still run the denied command.
func TestBashDenyRuleSeesThroughWrappers(t *testing.T) {
	d := &Data{}
	d.Permissions.Deny = []string{"Bash(curl:*)"}
	bypass := &SessionPermissions{Mode: ModeBypassPermissions}
	for _, cmd := range []string{
		`\curl x`, "env curl x", "env -i FOO=1 curl x", "command curl x", "exec curl x",
		"bash -c 'curl x'", "sh -ec 'curl x'", "nice env curl x", "bash -c \"echo; bash -c 'curl x'\"",
	} {
		if got := d.HasPermissionToUseTool("Bash", map[string]any{"command": cmd}, bypass); got.Behavior != perm.Reject {
			t.Errorf("%q = %v, want deny", cmd, got)
		}
	}
}

// Unwrapping env must not turn its assignments into a silent read.
func TestEnvAssignmentIsNotReadOnly(t *testing.T) {
	for _, cmd := range []string{"env GIT_EXTERNAL_DIFF=evil git diff", "env PAGER=evil git log", "bash -c 'ls'"} {
		if IsReadOnlyBashCommand(cmd) {
			t.Errorf("%q classified read-only", cmd)
		}
	}
	for _, cmd := range []string{"env ls", "nice ls"} {
		if !IsReadOnlyBashCommand(cmd) {
			t.Errorf("%q should stay read-only", cmd)
		}
	}
}
