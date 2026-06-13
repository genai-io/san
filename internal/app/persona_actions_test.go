package app

import "testing"

func TestSanitizePersonaName(t *testing.T) {
	cases := map[string]string{
		"  ML Researcher ": "ml-researcher",
		"go_reviewer":      "go-reviewer",
		"Foo!!!Bar":        "foobar",
		"--weird--":        "weird",
		"a  b":             "a-b",
		"":                 "",
		"default":          "default",
	}
	for in, want := range cases {
		if got := sanitizePersonaName(in); got != want {
			t.Errorf("sanitizePersonaName(%q) = %q, want %q", in, got, want)
		}
	}
}
