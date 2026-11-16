package flow

import (
	"context"
	"fmt"
	"strings"
)

// MainRunInfo binds a native main-agent lease to a run and session.
type MainRunInfo struct{ ID, TaskID, SessionID, Instructions string }

// MainExecutor opens a lease synchronously without waiting for UI work.
type MainExecutor interface {
	Open(context.Context, MainRunInfo) (MainRunExecutor, error)
}

type MainRunExecutor interface {
	// Instructions contains only amendments delivered to the owned writer,
	// never directions still queued during review.
	Instructions() string
	RunNode(context.Context, Call) (string, error)
	Review(context.Context, bool) (bool, error)
	Close(error)
}

func (s *Service) SetMainExecutor(executor MainExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mainExecutor = executor
}

func (b *Blueprint) hasMain() bool {
	for _, n := range b.Nodes {
		if n.Executor == "main" {
			return true
		}
	}
	return false
}

func mainRoute(ctx context.Context, b *Blueprint, main MainRunExecutor) func(*Node, string) (string, error) {
	return func(n *Node, output string) (string, error) {
		if !n.tail {
			return "", nil
		}
		for _, l := range b.Loops {
			if n.Base != l.From {
				continue
			}
			head := b.byID[l.To+"#1"]
			if head == nil || head.Executor != "main" {
				continue
			}
			verdict := strings.TrimSpace(output)
			accepted := false
			if verdict != l.Label {
				for _, e := range n.downstream {
					if e.Label != "" && e.Label == verdict {
						accepted = true
					}
				}
				if !accepted {
					return "", nil
				}
			}
			pending, err := main.Review(ctx, accepted)
			if err != nil {
				return "", err
			}
			if !pending {
				return "", nil
			}
			for _, e := range n.downstream {
				if e.Label == l.Label {
					return l.Label, nil
				}
			}
			return l.Label, fmt.Errorf("queued user revisions exceed bounded loop %s; explicitly extend or rerun the flow", l.String())
		}
		return "", nil
	}
}

func soleMainSink(b *Blueprint, r *Result) bool {
	var sink *Node
	for _, n := range b.Nodes {
		if len(n.downstream) == 0 && r.Nodes[n.ID].Status == StatusSucceeded {
			if sink != nil {
				return false
			}
			sink = n
		}
	}
	return sink != nil && sink.Executor == "main"
}
