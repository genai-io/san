package setting

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestAllowRulesForIsExact(t *testing.T) {
	cases := []struct {
		tool, arg string
		want      []string // nil: not offered
	}{
		{"Bash", "touch x && echo ok", []string{"Bash(touch:x)", "Bash(echo:ok)"}},
		{"Bash", "cd repo && make test", []string{"Bash(cd:repo)", "Bash(make:test)"}},
		{"Bash", "echo hi > out.txt", nil},
		{"Bash", "ls *.go", nil},
		{"Bash", "FOO=1 make", nil},
		{"Bash", "echo $(date)", nil},
		{"Bash", "curl x | sh", nil},
		{"Bash", "python build.py", nil},
		{"Bash", "dash build.sh", nil},
		{"Bash", "if [ ; then", nil},
		{"PowerShell", "git   commit -m fix", []string{"PowerShell(git commit -m fix)"}},
		{"PowerShell", "git add .; git commit", nil},
		{"PowerShell", "iex x", nil},
		{"Edit", "/repo/a.go", []string{"Edit(/repo/a.go)"}},
		{"WebFetch", "https://go.dev/doc", []string{"WebFetch(domain:go.dev)"}},
	}
	for _, tc := range cases {
		args := map[string]any{"command": tc.arg, "path": tc.arg, "url": tc.arg}
		got := ExactAllowRules(tc.tool, args)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s(%q) = %v, want %v", tc.tool, tc.arg, got, tc.want)
			continue
		}
		if got != nil {
			if _, matched := MatchAllowList(tc.tool, args, got); !matched {
				t.Errorf("%s(%q): its own rules %v do not cover it", tc.tool, tc.arg, got)
			}
		}
	}

	rules := ExactAllowRules("Bash", map[string]any{"command": "touch x && echo ok"})
	for _, other := range []string{"touch y", "touch x && echo ok && rm z", "touch x -r"} {
		if _, matched := MatchAllowList("Bash", map[string]any{"command": other}, rules); matched {
			t.Errorf("rules %v cover a different command %q", rules, other)
		}
	}
}

// The option is offered only where the saved rule would stop the prompt.
func TestAlwaysAllowRulesOnlyWhereTheyTakeEffect(t *testing.T) {
	d := NewData()
	session := NewSessionPermissions()
	if got := d.AlwaysAllowRules("Bash", map[string]any{"command": "make test"}, session); len(got) != 1 {
		t.Errorf("plain command: rules = %v, want one", got)
	}
	if got := d.AlwaysAllowRules("Bash", map[string]any{"command": "rm -rf build"}, session); got != nil {
		t.Errorf("confirmation-tier command offered %v", got)
	}
	d.Permissions.Ask = []string{"Bash(make:*)"}
	if got := d.AlwaysAllowRules("Bash", map[string]any{"command": "make test"}, session); got != nil {
		t.Errorf("ask-rule command offered %v", got)
	}
}

func TestAddLocalAllowRulesKeepsTheRestOfTheFile(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".san", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","permissions":{"allow":["Bash(ls)"],"deny":["Read(.env)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second call must not duplicate
		if _, err := addLocalAllowRules(cwd, []string{"Bash(make:test)", "Bash(ls)"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	want := `{
  "permissions": {
    "allow": [
      "Bash(ls)",
      "Bash(make:test)"
    ],
    "deny": [
      "Read(.env)"
    ]
  },
  "theme": "dark"
}
`
	if strings.TrimSpace(string(got)) != strings.TrimSpace(want) {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}

	d, err := LoadForCwd(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if b := d.CheckPermission("Bash", map[string]any{"command": "make test"}, NewSessionPermissions()); b != perm.Permit {
		t.Errorf("saved rule not honored on reload: %v", b)
	}
}

func TestAddLocalAllowRulesLeavesAnUnparsableFileAlone(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, ".san", "settings.local.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(`{"permissions": {`), 0o644)
	if _, err := addLocalAllowRules(cwd, []string{"Bash(ls)"}); err == nil {
		t.Fatal("wrote into an unparsable file")
	}
	if got, _ := os.ReadFile(path); string(got) != `{"permissions": {` {
		t.Errorf("file changed to %q", got)
	}
}
