package setting

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestFilePermissionsFollowSensitiveSymlinks(t *testing.T) {
	dir := t.TempDir()
	git := filepath.Join(dir, ".git")
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(git, "config")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "ordinary.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dirLink := filepath.Join(dir, "ordinary-dir")
	if err := os.Symlink(git, dirLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "new-link")
	if err := os.Symlink(filepath.Join(git, "missing-file"), dangling); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(git, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	innerLink := filepath.Join(dir, "inner-link")
	if err := os.Symlink(inner, innerLink); err != nil {
		t.Fatal(err)
	}
	traversal := innerLink + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "new-file"
	d := NewData()
	s := &SessionPermissions{Mode: ModeAutoAccept, WorkingDirectories: []string{dir}}
	for _, tool := range []string{"Write", "Edit"} {
		for _, path := range []string{target, link, dangling, traversal, filepath.Join(dirLink, "new-dir", "new-file")} {
			decision := d.HasPermissionToUseTool(tool, map[string]any{"file_path": path}, s)
			if decision.Behavior != perm.Prompt || decision.Reviewable {
				t.Errorf("%s(%s) = %+v, want human confirmation", tool, path, decision)
			}
		}
	}
	// A sensitive name remains sensitive even when it links to a harmless target.
	startup := filepath.Join(dir, ".zshrc")
	if err := os.Symlink(filepath.Join(dir, "missing"), startup); err != nil {
		t.Fatal(err)
	}
	if isSensitivePath(startup) == "" {
		t.Fatal("sensitive original name was ignored")
	}
}

func TestEditDenyUsesFilePath(t *testing.T) {
	d := NewData()
	d.Permissions.Deny = []string{"Edit(**/.env)"}
	s := &SessionPermissions{Mode: ModeAutoAccept}
	args := map[string]any{"file_path": "/repo/.env", "old_string": "old", "new_string": "new"}
	if got := d.HasPermissionToUseTool("Edit", args, s); got.Behavior != perm.Reject {
		t.Fatalf("Edit deny = %+v, want Reject", got)
	}
}

func TestIsReadOnlyBashCommand(t *testing.T) {
	readOnly := []string{
		"rg -n pattern ./src",
		"rg --ignore-case TODO | head -50",
		"grep -rn foo internal",
		"find . -name '*.go' -mtime -1",
		"fd -e go tool",
		"ls -la internal/tool",
		"cat go.mod | wc -l",
		"head -100 README.md",
		"tree -L 2 internal",
		"cd internal && rg -l Register",
		"git status",
		"git log --oneline -20",
		"git diff --stat && git status",
		"git show --stat HEAD",
		"which rg",
		"rg foo 2>/dev/null",
		"stat -f%z go.sum",
	}
	for _, cmd := range readOnly {
		if !IsReadOnlyBashCommand(cmd) {
			t.Errorf("IsReadOnlyBashCommand(%q) = false, want true", cmd)
		}
	}

	notReadOnly := []string{
		"",
		"rm -rf /tmp/x",
		"go build ./...",
		"npm install",
		"echo hi",
		"sed -i s/a/b/ file.go",
		"git commit -m msg",
		"git push origin main",
		"git diff --output=/tmp/diff.txt",
		"git diff --output /tmp/diff.txt",
		"git show --output=/tmp/show.txt HEAD",
		"git log --output=/tmp/log.txt",
		"git diff --ext-diff",
		"git diff --textconv",
		"ls > files.txt",                     // output redirection
		"cat foo 2>errors.log",               // stderr to a real file
		"rg $(cat cmds.txt)",                 // command substitution
		"cat `which rg`",                     // backtick substitution
		"diff <(sort a) <(sort b)",           // process substitution
		"find . -name '*.tmp' -delete",       // find write flag
		"find . -exec rm {} \\;",             // find exec flag
		"fd -x rm",                           // fd exec flag
		"rg --pre ./script foo",              // rg preprocessor executes
		"tree -o out.txt",                    // tree write flag
		"GIT_EXTERNAL_DIFF=evil git diff",    // env assignment redirects binary
		"PAGER=evil git log",                 // env assignment redirects binary
		"export FOO=bar",                     // declaration builtin
		"eval ls",                            // dangerous builtin
		"xargs rm < list.txt",                // executes arbitrary command
		"rg foo && rm -rf /tmp/x",            // mutating tail in chain
		"timeout 10 sh -c 'rm -rf /tmp/dir'", // wrapper hides shell
	}
	for _, cmd := range notReadOnly {
		if IsReadOnlyBashCommand(cmd) {
			t.Errorf("IsReadOnlyBashCommand(%q) = true, want false", cmd)
		}
	}
}

func TestModeDefaultForCallPermitsCronListing(t *testing.T) {
	if d := ModeDefaultForCall("Cron", map[string]any{"action": "list"}, ModeNormal); d.Behavior != perm.Permit {
		t.Fatalf("Cron list = %v, want Permit", d.Behavior)
	}
	if d := ModeDefaultForCall("Cron", map[string]any{"action": "create", "cron": "0 9 * * *", "prompt": "x"}, ModeNormal); d.Behavior == perm.Permit {
		t.Fatal("Cron create must not auto-permit")
	}
}
