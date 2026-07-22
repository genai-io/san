package setting

import (
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

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
