package trust

import (
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestCleanWorkspaceReview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	cwd := t.TempDir()
	if got := Findings(cwd); len(got) != 0 || setting.IsWorkspaceTrusted(cwd) {
		t.Fatalf("Findings must leave a clean workspace untrusted; findings: %v", got)
	}
	if got := ReviewAndTrustIfClean(cwd); len(got) != 0 || !setting.IsWorkspaceTrusted(cwd) {
		t.Fatalf("review must trust a clean workspace for this session; findings: %v", got)
	}
}
