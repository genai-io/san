package llm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/auth"

	"github.com/genai-io/san/internal/secret"
)

func TestSubscriptionRefreshAcrossProcesses(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%v", concurrent), func(t *testing.T) {
			isolateCredentials(t)
			if err := secret.Default().Set("OPENAI_CHATGPT_AUTH", credentialJSON(t, codexBlob{
				AccessToken: "old", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Hour),
			})); err != nil {
				t.Fatal(err)
			}
			var refreshes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth/token" {
					if r.Header.Get("Authorization") != "Bearer fresh" {
						t.Error("inference did not use the refreshed credential")
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if refreshes.Add(1) != 1 {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error":{"message":"Your refresh token has already been used to generate a new access token. Please try signing in again.","code":"refresh_token_reused"}}`)
					return
				}
				if concurrent {
					time.Sleep(100 * time.Millisecond)
				}
				fmt.Fprint(w, `{"access_token":"fresh","refresh_token":"r2","expires_in":3600}`)
			}))
			defer server.Close()
			first := startRefreshWorker(t, server.URL)
			second := startRefreshWorker(t, server.URL)
			first.resume(t)
			if !concurrent {
				first.wait(t)
			}
			second.resume(t)
			second.wait(t)
			if concurrent {
				first.wait(t)
			}
			if got := refreshes.Load(); got != 1 {
				t.Fatalf("refreshes = %d, want one exchange across both processes", got)
			}
		})
	}
}

type refreshWorker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
}

func startRefreshWorker(t *testing.T, target string) *refreshWorker {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	w := &refreshWorker{cmd: exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSubscriptionRefreshWorker$", "-test.timeout=10s")}
	w.cmd.Env = append(os.Environ(), "SAN_TEST_AUTH_URL="+target, "SAN_TEST_AUTH_HOME="+os.Getenv("HOME"))
	w.cmd.Stderr = &w.stderr
	var err error
	w.stdin, err = w.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	w.stdout = bufio.NewReader(out)
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if w.cmd.ProcessState == nil {
			_ = w.cmd.Process.Kill()
			_ = w.cmd.Wait()
		}
	})
	if line, err := w.stdout.ReadString('\n'); err != nil || strings.TrimSpace(line) != "cached" {
		t.Fatalf("worker did not cache credentials: %v %s", err, line)
	}
	return w
}

func (w *refreshWorker) resume(t *testing.T) {
	t.Helper()
	if _, err := fmt.Fprintln(w.stdin, "continue"); err != nil {
		t.Fatal(err)
	}
	_ = w.stdin.Close()
}

func (w *refreshWorker) wait(t *testing.T) {
	t.Helper()
	output, readErr := io.ReadAll(w.stdout)
	err := w.cmd.Wait()
	if err != nil || readErr != nil {
		t.Fatalf("refresh worker failed: %v %v\n%s%s", err, readErr, output, w.stderr.String())
	}
}

type redirectAuthTransport struct {
	target *url.URL
	base   *http.Transport
}

func (d redirectAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = d.target.Scheme, d.target.Host
	return d.base.RoundTrip(clone)
}

func TestSubscriptionRefreshWorker(t *testing.T) {
	target := os.Getenv("SAN_TEST_AUTH_URL")
	if target == "" {
		t.Skip("subprocess helper")
	}
	// TestMain isolates HOME per process; these workers intentionally share it.
	t.Setenv("HOME", os.Getenv("SAN_TEST_AUTH_HOME"))
	secret.ResetDefault()
	t.Cleanup(secret.ResetDefault)
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Transport{}
	t.Cleanup(base.CloseIdleConnections)
	previous := http.DefaultTransport
	http.DefaultTransport = redirectAuthTransport{target: u, base: base}
	t.Cleanup(func() { http.DefaultTransport = previous })
	c, found, err := (credentials{}).Load(codexVendor)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	fmt.Println("cached")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	client, err := auth.HTTPClient(codexVendor, c, credentials{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}
