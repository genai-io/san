package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/persona"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestPrintUsesEffectiveToolAndPermissionSettings(t *testing.T) {
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".san")
	if err := os.MkdirAll(filepath.Join(dir, "personas", "restricted"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), []byte(`{
		"disabledTools":{"Read":true},
		"permissions":{"allow":["Bash(echo:*)"],"deny":["Write(**/.env)"]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "personas", "restricted", "settings.json"), []byte(`{
		"disabledTools":{"Edit":true},
		"permissions":{"deny":["Bash(echo:blocked)"],"ask":["Bash(echo:review)"]}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := persona.Default()
	persona.SetDefault(persona.NewRegistry(cwd))
	t.Cleanup(func() { persona.SetDefault(old) })
	for _, name := range []string{"", "restricted"} {
		params, err := printBuildParams(cwd, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, schema := range params.Schemas() {
			if schema.Name == "Read" || (name != "" && schema.Name == "Edit") {
				t.Errorf("persona %q still offers disabled %s", name, schema.Name)
			}
		}
		if got := params.PermissionRules("Write", map[string]any{"file_path": "/repo/.env"}); got.Decision != perm.Reject {
			t.Errorf("persona %q dropped base deny: %+v", name, got)
		}
		if name != "" {
			for _, arg := range []string{"blocked", "review"} {
				got := params.PermissionRules("Bash", map[string]any{"command": "echo " + arg})
				if got.Decision != perm.Reject || !strings.Contains(got.Reason, "rule:") {
					t.Errorf("persona %q dropped %s rule: %+v", name, arg, got)
				}
			}
		}
	}
	if _, err := printBuildParams(cwd, "missing"); err == nil {
		t.Fatal("unknown persona accepted")
	}
}
