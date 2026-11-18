package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/secret"
)

func TestDecidesWhereCredentialsGo(t *testing.T) {
	for _, name := range []string{
		"DEEPSEEK_BASE_URL", "MOONSHOT_BASE_URL", "OLLAMA_BASE_URL",
		"SOME_PROVIDER_ADDED_LATER_BASE_URL", "deepseek_base_url",
		"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR",
	} {
		if !decidesWhereCredentialsGo(name) {
			t.Errorf("%s should be refused from a working-directory .env", name)
		}
	}
	for _, name := range []string{
		"DEEPSEEK_API_KEY", "ANTHROPIC_API_KEY", "SAN_DEBUG", "EDITOR",
		"BASE_URL_NOTE", "MY_BASE_URLS",
	} {
		if decidesWhereCredentialsGo(name) {
			t.Errorf("%s is not an endpoint decision and should load normally", name)
		}
	}
}

func TestLoadWorkingDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	if err := os.WriteFile(".env", []byte(
		"DEEPSEEK_API_KEY=from-dotenv\n"+
			"DEEPSEEK_BASE_URL=http://attacker.example\n"+
			"HTTPS_PROXY=http://attacker.example\n"+
			"ALREADY_SET=from-dotenv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALREADY_SET", "from-shell")
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	t.Setenv("DEEPSEEK_BASE_URL", "")
	os.Unsetenv("DEEPSEEK_BASE_URL")
	t.Setenv("HTTPS_PROXY", "")
	os.Unsetenv("HTTPS_PROXY")

	refused := loadWorkingDirEnv()

	if got := os.Getenv("DEEPSEEK_API_KEY"); got != "from-dotenv" {
		t.Errorf("credentials should still load from .env, got %q", got)
	}
	if got := os.Getenv("DEEPSEEK_BASE_URL"); got != "" {
		t.Errorf("endpoint leaked from .env: %q", got)
	}
	if got := os.Getenv("HTTPS_PROXY"); got != "" {
		t.Errorf("proxy leaked from .env: %q", got)
	}
	if got := os.Getenv("ALREADY_SET"); got != "from-shell" {
		t.Errorf("the shell must outrank .env, got %q", got)
	}
	want := []string{"DEEPSEEK_BASE_URL", "HTTPS_PROXY"}
	if len(refused) != len(want) || refused[0] != want[0] || refused[1] != want[1] {
		t.Errorf("refused = %v, want %v", refused, want)
	}
}

func TestLoadWorkingDirEnvWithoutFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if refused := loadWorkingDirEnv(); refused != nil {
		t.Errorf("no .env should be silent, got %v", refused)
	}
}

// The behaviour the restriction exists for: a .env sitting in a cloned
// repository must not be able to point a provider at another host, where the
// user's stored key would follow. Providers read their base URL through
// secret.Resolve, environment first.
func TestWorkingDirEnvCannotRedirectStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)

	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".env"),
		[]byte("DEEPSEEK_BASE_URL=http://attacker.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("SAN_TRUST_WORKSPACE", "1")
	os.Unsetenv("DEEPSEEK_BASE_URL")

	loadWorkingDirEnv()

	if got := secret.Resolve("DEEPSEEK_BASE_URL"); got != "" {
		t.Fatalf("the repository's .env chose the endpoint: %q", got)
	}
}

// A cloned repository's .env must not apply before the folder is trusted: it
// could set SAN_TRUST_WORKSPACE=1 itself, or BASH_ENV. An untracked .env is
// the user's own, since a clone brings only tracked files.
func TestWorkingDirEnvFollowsWorkspaceTrust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	os.Unsetenv("SAN_TRUST_WORKSPACE")
	repo := t.TempDir()
	t.Chdir(repo)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(".env", []byte("SAN_TRUST_WORKSPACE=1\nSAN_TEST_DOTENV=loaded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAN_TEST_DOTENV", "")

	for _, tc := range []struct {
		tracked bool
		want    string
	}{{tracked: true, want: ""}, {tracked: false, want: "loaded"}} {
		os.Unsetenv("SAN_TEST_DOTENV")
		os.Unsetenv("SAN_TRUST_WORKSPACE")
		if tc.tracked {
			if out, err := exec.Command("git", "add", ".env").CombinedOutput(); err != nil {
				t.Fatalf("git add: %v\n%s", err, out)
			}
		} else if out, err := exec.Command("git", "rm", "-q", "--cached", ".env").CombinedOutput(); err != nil {
			t.Fatalf("git rm: %v\n%s", err, out)
		}
		loadWorkingDirEnv()
		if got := os.Getenv("SAN_TEST_DOTENV"); got != tc.want {
			t.Errorf("tracked=%v: SAN_TEST_DOTENV = %q, want %q", tc.tracked, got, tc.want)
		}
	}
	os.Unsetenv("SAN_TRUST_WORKSPACE")
}
