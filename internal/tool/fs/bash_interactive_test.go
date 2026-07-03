//go:build unix

package fs

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type fakeResponder struct {
	answer      string
	answerOK    bool
	secret      string
	secretOK    bool
	answerCalls int
	secretCalls int
	lastPrompt  string
}

func (f *fakeResponder) AnswerPrompt(_ context.Context, _, prompt string) (string, bool) {
	f.answerCalls++
	f.lastPrompt = prompt
	return f.answer, f.answerOK
}

func (f *fakeResponder) RequestSecret(_ context.Context, prompt string) (string, bool) {
	f.secretCalls++
	f.lastPrompt = prompt
	return f.secret, f.secretOK
}

func runScript(t *testing.T, script string, r PromptResponder) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.Command("bash", "-c", script)
	out, _ := runInteractive(ctx, script, cmd, r)
	return out
}

func Test_runInteractive_answersNonSecretPrompt(t *testing.T) {
	r := &fakeResponder{answer: "yes", answerOK: true}
	out := runScript(t, `read -p "Continue? [y/N] " a; echo "answer=$a"`, r)

	if !strings.Contains(out, "answer=yes") {
		t.Errorf("output %q missing answer=yes", out)
	}
	if r.answerCalls != 1 || r.secretCalls != 0 {
		t.Errorf("calls: answer=%d secret=%d, want 1/0", r.answerCalls, r.secretCalls)
	}
	if !strings.Contains(r.lastPrompt, "Continue?") {
		t.Errorf("prompt seen = %q, want the Continue? prompt", r.lastPrompt)
	}
}

func Test_runInteractive_secretPromptUsesRequestSecret(t *testing.T) {
	r := &fakeResponder{secret: "hunter2", secretOK: true}
	out := runScript(t, `read -s -p "Password: " p; echo; echo "len=${#p}"`, r)

	if !strings.Contains(out, "len=7") {
		t.Errorf("output %q missing len=7 (secret not delivered)", out)
	}
	if r.secretCalls != 1 || r.answerCalls != 0 {
		t.Errorf("calls: secret=%d answer=%d, want 1/0 (password must not go to AnswerPrompt)", r.secretCalls, r.answerCalls)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("secret value leaked into output: %q", out)
	}
}

func Test_runInteractive_skipFailsFast(t *testing.T) {
	r := &fakeResponder{answerOK: false} // decline to answer
	out := runScript(t, `read -p "x? " a; echo "got=$a"`, r)

	if r.answerCalls != 1 {
		t.Errorf("answerCalls = %d, want 1", r.answerCalls)
	}
	if strings.Contains(out, "got=") {
		t.Errorf("skip must not feed input, but the command proceeded: %q", out)
	}
}

func Test_lastLine(t *testing.T) {
	cases := map[string]string{
		"Password: ":                 "Password:",
		"foo\nbar\nContinue? [y/N] ":  "Continue? [y/N]",
		"line\r\nprompt> ":            "prompt>",
		"trailing\n\n\n":              "trailing",
		"":                           "",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func Test_isSecretPrompt(t *testing.T) {
	secret := []string{"Password:", "[sudo] password for me:", "Enter passphrase for key:", "Enter PIN:"}
	notSecret := []string{"Continue? [y/N]", "Overwrite existing file?", "Proceed (yes/no)?"}
	for _, p := range secret {
		if !isSecretPrompt(p) {
			t.Errorf("isSecretPrompt(%q) = false, want true", p)
		}
	}
	for _, p := range notSecret {
		if isSecretPrompt(p) {
			t.Errorf("isSecretPrompt(%q) = true, want false", p)
		}
	}
}
