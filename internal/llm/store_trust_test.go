package llm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func trustModelFixture(t *testing.T, gitRepo, tracked bool) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if gitRepo {
		run("init", "--quiet")
	}
	path := filepath.Join(root, ".san", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"current":{"modelID":"project-model","provider":"openai","authMethod":"api_key"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if tracked {
		run("add", ".san/providers.json")
	}
	user, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := user.SetCurrentModel("user-model", OpenAI, AuthAPIKey); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestProjectModelTrustAndReload(t *testing.T) {
	for _, gitRepo := range []bool{false, true} {
		name := "nonGit"
		if gitRepo {
			name = "trackedGit"
		}
		t.Run(name, func(t *testing.T) {
			root, _ := trustModelFixture(t, gitRepo, gitRepo)
			store, err := NewStoreForCwd(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := store.CurrentModel(); got == nil || got.ModelID != "user-model" {
				t.Fatalf("untrusted current = %+v", got)
			}
			findings := ProjectModelFindings(root)
			if len(findings) != 1 || !strings.Contains(findings[0], "project-model") || !strings.Contains(findings[0], "openai") || !strings.Contains(findings[0], "api_key") {
				t.Fatalf("findings = %v", findings)
			}
			if err := setting.TrustWorkspace(root); err != nil {
				t.Fatal(err)
			}
			if err := store.Reload(); err != nil {
				t.Fatal(err)
			}
			if got := store.CurrentModel(); got == nil || got.ModelID != "project-model" {
				t.Fatalf("trusted reloaded current = %+v", got)
			}
			reopened, err := NewStoreForCwd(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := reopened.CurrentModel(); got == nil || got.ModelID != "project-model" {
				t.Fatalf("trusted current = %+v", got)
			}
		})
	}
}

func TestPersonalUntrackedProjectModelEligibleWithoutTrust(t *testing.T) {
	root, _ := trustModelFixture(t, true, false)
	if setting.IsWorkspaceTrusted(root) {
		t.Fatal("fixture unexpectedly trusted")
	}
	store, err := NewStoreForCwd(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.CurrentModel(); got == nil || got.ModelID != "project-model" {
		t.Fatalf("personal current = %+v", got)
	}
	if findings := ProjectModelFindings(root); len(findings) != 0 {
		t.Fatalf("personal findings = %v", findings)
	}
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := store.CurrentModel(); got == nil || got.ModelID != "project-model" {
		t.Fatalf("personal reloaded current = %+v", got)
	}
}

func TestMalformedUntrustedProjectModelCannotBeClobbered(t *testing.T) {
	root, path := trustModelFixture(t, true, true)
	malformed := []byte(`{"current":`)
	if err := os.WriteFile(path, malformed, 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreForCwd(root)
	if err != nil {
		t.Fatalf("ignored untrusted malformed file: %v", err)
	}
	if err := store.SetCurrentModel("replacement", OpenAI, AuthAPIKey); err == nil {
		t.Fatal("switch overwrote malformed project model")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(malformed) {
		t.Fatalf("malformed file changed: %q", contents)
	}
	if got := store.CurrentModel(); got == nil || got.ModelID != "user-model" {
		t.Fatalf("failed switch changed current: %+v", got)
	}
}
