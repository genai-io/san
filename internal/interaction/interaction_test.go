package interaction

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/tool"
)

type testOwner struct{ closed atomic.Bool }

func (o *testOwner) Closed() bool { return o.closed.Load() }

func question(ctx context.Context, owner Owner, id string) *Request {
	return &Request{Context: ctx, Owner: owner, Question: &Question{
		Request: &tool.QuestionRequest{ID: id}, Reply: make(chan *tool.QuestionResponse, 1),
	}}
}

func TestQueueLifetimeAndOrder(t *testing.T) {
	owner := &testOwner{}
	foreground, stop := context.WithCancel(context.Background())
	defer stop()
	first := question(context.Background(), owner, "first")
	expired := question(foreground, owner, "foreground")
	background := question(context.Background(), owner, "background")
	var q Queue
	for _, r := range []*Request{first, expired, background} {
		if !q.Enqueue(r, owner) {
			t.Fatal("live request rejected")
		}
	}
	stop()
	if !q.Prune(owner) || q.Prune(owner) {
		t.Fatal("unexpected prune change")
	}
	if reply := <-expired.Question.Reply; !reply.Cancelled || reply.RequestID != "foreground" {
		t.Fatal(reply)
	}
	if q.Pop() != first || q.Pop() != background || q.Pop() != nil {
		t.Fatal("FIFO order changed")
	}
	if len(background.Question.Reply) != 0 {
		t.Fatal("background cancelled with foreground")
	}
}

func TestOwnerGenerationAndClosure(t *testing.T) {
	old, current := &testOwner{}, &testOwner{}
	stale := question(context.Background(), old, "old")
	var q Queue
	if q.Enqueue(stale, current) || len(stale.Question.Reply) != 1 {
		t.Fatal("stale generation accepted")
	}
	live := question(context.Background(), current, "live")
	q.Enqueue(live, current)
	current.closed.Store(true)
	if live.Valid(current) || !q.Prune(current) || len(q) != 0 || len(live.Question.Reply) != 1 {
		t.Fatal("closed owner survived")
	}
	if (*Request)(nil).Valid(current) {
		t.Fatal("nil request valid")
	}
	if !(&Request{}).Valid(current) {
		t.Fatal("ownerless legacy request rejected")
	}
}

func TestCancellationResponses(t *testing.T) {
	permission := &Request{Permission: &agent.PermGateRequest{Response: make(chan agent.PermGateResponse, 1)}}
	ask := question(context.Background(), nil, "question")
	secret := &Request{Secret: &Secret{Reply: make(chan SecretResponse, 1)}}
	q := Queue{permission, ask, secret, nil}
	q.Cancel()
	if q != nil {
		t.Fatal("queue retained")
	}
	// Full reply channels must not block cancellation or overwrite an answer.
	permission.Cancel()
	ask.Cancel()
	secret.Cancel()
	if r := <-permission.Permission.Response; r.Allow || r.Reason != "interaction cancelled" {
		t.Fatal(r)
	}
	if r := <-ask.Question.Reply; !r.Cancelled || r.RequestID != "question" {
		t.Fatal(r)
	}
	if r := <-secret.Secret.Reply; !r.Cancelled || r.Value != "" {
		t.Fatal("invalid secret cancellation")
	}
}

func TestConcurrentContextCancellation(t *testing.T) {
	owner := &testOwner{}
	ctx, cancel := context.WithCancel(context.Background())
	r := question(ctx, owner, "concurrent")
	done := make(chan struct{})
	go func() { cancel(); owner.closed.Store(true); close(done) }()
	for range 100 {
		_ = r.Valid(owner)
	}
	<-done
	if r.Valid(owner) {
		t.Fatal("cancelled request valid")
	}
}
