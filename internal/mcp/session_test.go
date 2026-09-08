package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/genai-io/sdk-go/pkg/agent/mcp"

	"github.com/genai-io/san/internal/core"
)

// fakeSession stands in for a running MCP server: alive until Close.
//
// The registry's tests are about leases, epochs and replacement, and none of
// them is about the protocol. Standing a real server up per case would test
// the SDK's transport over and over — it has its own tests for that, against a
// real child process — while making these ones slow and flaky.
type fakeSession struct {
	// closeDelay is how long a teardown takes, for the tests about not
	// blocking on one.
	closeDelay time.Duration
	// dead makes a session that connects and is immediately not alive, which
	// is what the registry replaces.
	dead bool

	tools     []core.Tool
	resources []sdkmcp.Resource

	mu         sync.Mutex
	closed     bool
	closeCalls int
	closedCh   chan struct{}
}

func newFakeSession() *fakeSession { return &fakeSession{closedCh: make(chan struct{})} }

func newSlowSession(d time.Duration) *fakeSession {
	s := newFakeSession()
	s.closeDelay = d
	return s
}

func (s *fakeSession) Tools(context.Context) ([]core.Tool, error) { return s.tools, nil }

func (s *fakeSession) Resources(context.Context) ([]sdkmcp.Resource, error) { return s.resources, nil }

func (s *fakeSession) Prompts(context.Context) ([]sdkmcp.Prompt, error) { return nil, nil }

func (s *fakeSession) Alive() bool {
	if s.dead {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

func (s *fakeSession) Close() error {
	time.Sleep(s.closeDelay)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if s.closedCh != nil {
			close(s.closedCh)
		}
	}
	s.closeCalls++
	return nil
}

// done closes when this session is torn down, for the tests about a teardown
// that outlives the call that started it.
func (s *fakeSession) done() <-chan struct{} { return s.closedCh }

func (s *fakeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *fakeSession) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCalls
}

// dialing is the seam: a client that reaches this session instead of a server.
func dialing(s *fakeSession) func(context.Context, func()) (conn, error) {
	return func(context.Context, func()) (conn, error) { return s, nil }
}

// connectedClient is a client that has been through Connect, so the state it
// holds is the state connecting actually produces.
func connectedClient(t *testing.T, cfg ServerConfig, s *fakeSession) *Client {
	t.Helper()
	c := NewClient(cfg)
	c.dial = dialing(s)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect(%s): %v", cfg.Name, err)
	}
	return c
}
