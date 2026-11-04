package cli_test

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestACPStartupReservesStdout(t *testing.T) {
	bin := buildBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "acp")
	cmd.Dir = t.TempDir()
	cmd.Env = append(filteredEnv("HOME", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "MOONSHOT_API_KEY", "ALIBABA_API_KEY", "BIGMODEL_API_KEY", "ANTHROPIC_VERTEX_PROJECT_ID"), "HOME="+t.TempDir())
	// Keep stdin open: entering the root command's piped-prompt path would
	// hang here rather than starting the protocol host.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("acp consumed stdin as a print prompt")
	}
	if err == nil {
		t.Fatal("unconfigured provider unexpectedly accepted")
	}
	if stdout.Len() != 0 {
		t.Fatalf("non-protocol stdout: %q", stdout.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "provider") {
		t.Fatalf("missing stderr diagnostic: %q", stderr.String())
	}
}
