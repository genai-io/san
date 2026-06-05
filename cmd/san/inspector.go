package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/genai-io/san/v2/internal/confdir"
	"github.com/genai-io/san/v2/internal/inspector"
	"github.com/genai-io/san/v2/internal/session"
)

var (
	inspectorAddr   string
	inspectorNoOpen bool
)

func init() {
	inspectorCmd.Flags().StringVar(&inspectorAddr, "addr", "127.0.0.1:0", "Bind address (127.0.0.1 only; do not expose externally)")
	inspectorCmd.Flags().BoolVar(&inspectorNoOpen, "no-open", false, "Print the URL but don't launch the browser")
	rootCmd.AddCommand(inspectorCmd)
}

var inspectorCmd = &cobra.Command{
	Use:   "inspector",
	Short: "Open the local inspector for this project's sessions",
	Long: `Launch a localhost web server that visualizes session transcripts
recorded under ~/.san/projects/<encoded-cwd>/transcripts/. The inspector
is read-only and runs until Ctrl-C.

By default the server binds 127.0.0.1 on a random port and opens the page
in your default browser. Use --no-open to skip the browser launch and
--addr to pin a port (e.g. --addr 127.0.0.1:38080).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		projectDir := projectDirFor(cwd)

		// Sanity: warn if no transcripts have been recorded yet but still serve.
		txDir := filepath.Join(projectDir, "transcripts")
		if _, err := os.Stat(txDir); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Note: no transcripts found at %s — start a san session to populate it.\n", txDir)
		}

		// Refuse to bind to anything that isn't loopback — guards against a
		// fat-fingered --addr that would expose conversation history.
		if err := requireLoopback(inspectorAddr); err != nil {
			return err
		}

		ln, err := net.Listen("tcp", inspectorAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", inspectorAddr, err)
		}
		url := fmt.Sprintf("http://%s", ln.Addr().String())

		srv := &http.Server{Handler: inspector.New(projectDir).Handler()}

		// Shutdown on SIGINT/SIGTERM.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
		}()

		fmt.Printf("san inspector: serving %s\n  project: %s\n", url, projectDir)
		if !inspectorNoOpen {
			openBrowser(url)
		}

		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}

// projectDirFor returns ~/.san/projects/<encoded-cwd>. Mirrors
// session.encodePath without importing internals.
func projectDirFor(cwd string) string {
	home, _ := os.UserHomeDir()
	encoded := session.EncodePath(cwd)
	return filepath.Join(confdir.Dir(home), "projects", encoded)
}

// requireLoopback rejects any bind address whose host isn't 127.0.0.1/::1.
// Localhost-only is the security model — the transcripts contain everything
// the model saw, including any secrets in tool inputs.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return nil
	}
	return fmt.Errorf("--addr %q is not loopback; only 127.0.0.1, ::1, or localhost are allowed", addr)
}

func openBrowser(url string) {
	var bin string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		bin, args = "open", []string{url}
	case "linux":
		bin, args = "xdg-open", []string{url}
	case "windows":
		bin, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return
	}
	_ = exec.Command(bin, args...).Start()
}
