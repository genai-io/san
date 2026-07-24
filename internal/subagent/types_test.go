package subagent

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToolListUnmarshalCanonicalForms(t *testing.T) {
	var cfg struct {
		AllowTools ToolList `yaml:"allow_tools"`
		DenyTools  ToolList `yaml:"deny_tools"`
	}
	err := yaml.Unmarshal([]byte(`
allow_tools:
  - Read
  - Bash(git diff*)
  - {name: Bash, pattern: "git log:*"}
deny_tools:
  Bash: "rm:*"
  Write: true
`), &cfg)
	if err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if got := cfg.AllowTools.Names(); len(got) != 3 || got[0] != "Read" || got[1] != "Bash" || got[2] != "Bash" {
		t.Fatalf("AllowTools.Names() = %#v", got)
	}
	if !cfg.AllowTools.HasPattern("Bash") {
		t.Fatal("expected Bash to have pattern constraint in allow_tools")
	}
	if got := cfg.DenyTools.BareNames(); len(got) != 1 || got[0] != "Write" {
		t.Fatalf("DenyTools.BareNames() = %#v", got)
	}
	if !cfg.DenyTools.HasPattern("Bash") {
		t.Fatal("expected Bash to have pattern constraint in deny_tools")
	}
}

func TestToolListUnmarshalCommaString(t *testing.T) {
	var cfg struct {
		AllowTools ToolList `yaml:"allow_tools"`
	}
	err := yaml.Unmarshal([]byte(`
allow_tools: "Read, Glob, Bash(git diff*)"
`), &cfg)
	if err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	got := cfg.AllowTools.DisplayNames()
	want := []string{"Read", "Glob", "Bash(git diff*)"}
	if len(got) != len(want) {
		t.Fatalf("DisplayNames() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DisplayNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseRuleStringHandlesPatternForms(t *testing.T) {
	cases := []struct {
		in      string
		name    string
		pattern string
	}{
		{"Read", "Read", ""},
		{"Bash(git diff*)", "Bash", "git diff*"},
		{"WebFetch(domain:github.com)", "WebFetch", "domain:github.com"},
		{"  Edit  ", "Edit", ""},
	}
	for _, tc := range cases {
		got, ok := parseRuleString(tc.in)
		if !ok {
			t.Fatalf("parseRuleString(%q) returned !ok", tc.in)
		}
		if got.Name != tc.name || got.Pattern != tc.pattern {
			t.Fatalf("parseRuleString(%q) = %+v, want {%q %q}", tc.in, got, tc.name, tc.pattern)
		}
	}
}
