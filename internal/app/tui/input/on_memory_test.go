package input

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		size     int64
		expected string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{2048, "2.0KB"},
		{1024 * 1024, "1.0MB"},
		{1536 * 1024, "1.5MB"},
	}

	for _, tc := range tests {
		result := formatFileSize(tc.size)
		if result != tc.expected {
			t.Errorf("formatFileSize(%d) = %s, expected %s", tc.size, result, tc.expected)
		}
	}
}

func TestSetAutoMemoryScopes(t *testing.T) {
	t.Setenv("SAN_TRUST_WORKSPACE", "1") // project settings apply in a trusted folder
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_DISABLE_AUTO_MEMORY", "")
	proj := t.TempDir()
	t.Chdir(t.TempDir())
	s := setting.New(setting.NewData())

	if err := setAutoMemory(s, proj, false, setting.ScopeProject); err != nil {
		t.Fatalf("project off: %v", err)
	}
	if s.AutoMemory() || autoMemorySource(proj) != "this project" {
		t.Fatalf("want off from this project, got %v from %s", s.AutoMemory(), autoMemorySource(proj))
	}

	// The project setting outranks the user one, and the error says so.
	err := setAutoMemory(s, proj, true, setting.ScopeUser)
	if err == nil || !strings.Contains(err.Error(), "this project keeps it off") {
		t.Fatalf("user on under a project off: err = %v", err)
	}

	if err := setAutoMemory(s, proj, true, setting.ScopeProject); err != nil || !s.AutoMemory() {
		t.Fatalf("project on: err = %v, on = %v", err, s.AutoMemory())
	}
}
