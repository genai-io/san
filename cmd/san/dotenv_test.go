package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/genai-io/san/internal/setting"
)

func TestWorkingDirEnvPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JIRA_BASE_URL", "https://default.example")
	t.Setenv("SAN_TEST_EMPTY", "default")
	t.Setenv("SAN_TEST_UNLISTED", "unchanged")
	loadWorkingDirEnv()
	if got := os.Getenv("JIRA_BASE_URL"); got != "https://default.example" {
		t.Fatalf("without file: %q", got)
	}
	if err := os.WriteFile(".env", []byte("JIRA_BASE_URL=https://project.example\nSAN_TEST_EMPTY=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loadWorkingDirEnv()
	for k, want := range map[string]string{"JIRA_BASE_URL": "https://project.example", "SAN_TEST_EMPTY": "", "SAN_TEST_UNLISTED": "unchanged"} {
		if got, ok := os.LookupEnv(k); !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", k, got, ok, want)
		}
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
	os.Unsetenv("SAN_TEST_DOTENV")
	if out, err := exec.Command("git", "add", ".env").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if !setting.WorkingDirEnvNeedsTrust(repo) {
		t.Fatal("tracked .env should appear in workspace trust review")
	}
	if err := setting.TrustWorkspace(repo); err != nil {
		t.Fatal(err)
	}
	loadWorkingDirEnv()
	if got := os.Getenv("SAN_TEST_DOTENV"); got != "loaded" {
		t.Fatalf("after trusting workspace: SAN_TEST_DOTENV = %q", got)
	}
}

func TestWorkingDirEnvOutsideGit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("SAN_TRUST_WORKSPACE", "")
	t.Setenv("SAN_TEST_DOTENV", "")
	t.Chdir(t.TempDir())
	os.Unsetenv("SAN_TEST_DOTENV")
	if err := os.WriteFile(".env", []byte("SAN_TEST_DOTENV=local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadWorkingDirEnv()
	if got := os.Getenv("SAN_TEST_DOTENV"); got != "local" {
		t.Fatalf("local environment = %q, want local", got)
	}
}
