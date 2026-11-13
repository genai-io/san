package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/genai-io/san/internal/inspector"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/session"
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
	Long: `Launch a localhost web server that visualizes typed session events
recorded under the registered project's sessions/<sid>/events.jsonl. The inspector
is read-only and runs until Ctrl-C.

By default the server binds 127.0.0.1 on a random port and opens the page
in your default browser. Use --no-open to skip the browser launch and
--addr to pin a port (e.g. --addr 127.0.0.1:38080).`,
	RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		store, err := session.OpenStore(cmd.Context(), cwd)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, store.Close()) }()
		projectDir := store.ProjectDir()

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

		srv := &http.Server{Handler: inspector.New(store).Handler()}

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
			_ = proc.OpenURL(url)
		}

		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}

// requireLoopback rejects any bind address whose host isn't 127.0.0.1/::1.
// Localhost-only is the security model — the event logs contain everything
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
