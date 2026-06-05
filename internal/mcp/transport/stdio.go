package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/genai-io/san/internal/proc"
)

// STDIOConfig contains configuration for STDIO transport
type STDIOConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

// STDIOTransport implements Transport for STDIO-based MCP servers.
// It spawns a subprocess and communicates via stdin/stdout.
type STDIOTransport struct {
	config  STDIOConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner

	mu            sync.Mutex
	pending       map[uint64]chan *JSONRPCResponse
	alive         bool
	notifyHandler NotificationHandler
	readLoopDone  chan struct{}
}

// NewSTDIOTransport creates a new STDIO transport
func NewSTDIOTransport(config STDIOConfig) *STDIOTransport {
	return &STDIOTransport{
		config:       config,
		pending:      make(map[uint64]chan *JSONRPCResponse),
		readLoopDone: make(chan struct{}),
	}
}

// Start spawns the subprocess and establishes communication
func (t *STDIOTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.alive {
		t.mu.Unlock()
		return fmt.Errorf("STDIO transport already started")
	}
	t.mu.Unlock()

	// Expand environment variables in config
	command := expandEnv(t.config.Command)
	args := expandEnvSlice(t.config.Args)
	env := expandEnvMap(t.config.Env)

	// Build command
	t.cmd = exec.CommandContext(ctx, command, args...)
	t.cmd.Env = buildEnv(env)

	// Set up process group for clean termination (no-op on Windows).
	proc.SetProcessGroup(t.cmd)

	// Connect stdin/stdout
	var err error
	t.stdin, err = t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	t.stdout, err = t.cmd.StdoutPipe()
	if err != nil {
		_ = t.stdin.Close()
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Redirect stderr to our stderr for debugging
	t.cmd.Stderr = os.Stderr

	// Start the process
	if err := t.cmd.Start(); err != nil {
		_ = t.stdin.Close()
		_ = t.stdout.Close()
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	t.scanner = bufio.NewScanner(t.stdout)
	// Allow for large messages (up to 10MB)
	const maxScannerBuffer = 10 * 1024 * 1024
	t.scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)

	t.alive = true

	// Start read loop
	go t.readLoop()

	return nil
}

// readLoop continuously reads responses from stdout
func (t *STDIOTransport) readLoop() {
	defer close(t.readLoopDone)

	for t.scanner.Scan() {
		line := t.scanner.Text()
		if line == "" {
			continue
		}

		// Snapshot the handler under lock to avoid racing with SetNotificationHandler.
		t.mu.Lock()
		handler := t.notifyHandler
		t.mu.Unlock()

		// Try to parse as response
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Could be a notification or malformed message
			parseAndDispatchNotification([]byte(line), handler)
			continue
		}

		// Check if this is a notification (no ID)
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			parseAndDispatchNotification([]byte(line), handler)
			continue
		}

		// Find pending request
		t.mu.Lock()
		ch, ok := t.pending[resp.ID]
		if ok {
			delete(t.pending, resp.ID)
		}
		t.mu.Unlock()

		if ok {
			ch <- &resp
		}
	}

	// Scanner finished - mark as not alive
	t.mu.Lock()
	t.alive = false
	// Close all pending channels
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.mu.Unlock()
}

// writeJSON marshals and writes JSON data to stdin with a newline
func (t *STDIOTransport) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	t.mu.Lock()
	_, err = t.stdin.Write(append(data, '\n'))
	t.mu.Unlock()

	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	return nil
}

// Send sends a request and waits for response
func (t *STDIOTransport) Send(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if !t.IsAlive() {
		return nil, fmt.Errorf("transport is not connected")
	}

	respCh := make(chan *JSONRPCResponse, 1)

	t.mu.Lock()
	t.pending[req.ID] = respCh
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
	}()

	if err := t.writeJSON(req); err != nil {
		return nil, err
	}

	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	select {
	case resp := <-respCh:
		if resp == nil {
			return nil, fmt.Errorf("connection closed")
		}
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SendNotification sends a notification (no response expected)
func (t *STDIOTransport) SendNotification(ctx context.Context, notif *JSONRPCNotification) error {
	if !t.IsAlive() {
		return fmt.Errorf("transport is not connected")
	}
	return t.writeJSON(notif)
}

// Close terminates the subprocess and cleans up
func (t *STDIOTransport) Close() error {
	t.mu.Lock()
	t.alive = false
	t.mu.Unlock()

	// Close stdin to signal EOF
	if t.stdin != nil {
		_ = t.stdin.Close()
	}

	// Wait for read loop to finish
	select {
	case <-t.readLoopDone:
	case <-time.After(2 * time.Second):
	}

	// Terminate process. On Unix this signals the whole group so the MCP
	// server's own children get the signal too; on Windows TerminateGroup
	// ignores the signal and hard-kills the direct child immediately, which
	// makes the 5s wait below a no-op rather than wasted shutdown latency.
	if t.cmd != nil && t.cmd.Process != nil {
		_ = proc.TerminateGroup(t.cmd, syscall.SIGTERM)

		done := make(chan error, 1)
		go func() {
			done <- t.cmd.Wait()
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = proc.TerminateGroup(t.cmd, syscall.SIGKILL)
			<-done
		}
	}

	return nil
}

// IsAlive returns true if the transport is connected
func (t *STDIOTransport) IsAlive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.alive
}

// SetNotificationHandler sets the handler for incoming notifications
func (t *STDIOTransport) SetNotificationHandler(handler NotificationHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notifyHandler = handler
}
