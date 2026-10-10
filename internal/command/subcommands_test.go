package command

import (
	"slices"
	"testing"
)

func TestMatchingCompletesSubcommandsByLevel(t *testing.T) {
	r := &Registry{}
	for query, want := range map[string][]string{
		"/workflow ":           {"workflow list", "workflow show", "workflow run", "workflow stop"},
		"/workflow sh":         {"workflow show"},
		"/plugin marketplace ": {"plugin marketplace list", "plugin marketplace add", "plugin marketplace remove", "plugin marketplace sync"},
		"/memory edit g":       {"memory edit global"},
		"/workflow show ":      nil,
	} {
		var got []string
		for _, info := range r.Matching(query) {
			got = append(got, info.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Matching(%q) = %v, want %v", query, got, want)
		}
	}
}
