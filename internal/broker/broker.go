// Package broker routes messages between agents. It is the small piece that
// sits between a sender and a recipient so neither has to know how to reach
// the other: an agent registers an address to receive at; a sender hands the
// broker a message stamped with a destination, and the broker delivers it to
// whoever holds that address.
//
//   - The main conversation registers under Main; each background subagent
//     registers under its (unique) task id when it starts and unregisters
//     when it stops.
//   - Send routes a Message to its To address. A message to an address no one
//     holds is dropped, like a call to a number that's no longer in service.
//
// SendCompletion additionally retains task completion mail for an absent or
// rejecting owner until it registers again or wakes it with WakeCompletions.
// Retention is process-local; there are no topics, broadcasts, fallback
// recipients, or agent startup side effects.
// What delivered mail does is the recipient's business.
package broker

import (
	"errors"
	"sync"
)

// Main is the well-known address of the main conversation.
const Main = "main"

// Message is one routed message. From/To are agent addresses; Subject is a
// short human-facing notice (may be empty); Content is the body delivered to
// the recipient.
type Message struct {
	SourceSession string // native originating message; empty for host-generated mail
	SourceSeq     uint64
	SessionID     string // owning main conversation, retained across queued delivery
	From          string
	To            string
	Subject       string
	Content       string
	// TaskCompletion is routing metadata, independent of the model-facing body.
	// The broker passes it through without interpreting task lifecycle state.
	TaskCompletion *TaskCompletion
}

// TaskCompletion identifies a finished execution, not the whole task lifetime.
// A nil Message.TaskCompletion denotes ordinary mail.
type TaskCompletion struct {
	TaskID  string
	Attempt int
}

// Deliver hands a routed message to a recipient. Callbacks run outside the
// global broker lock and may synchronously persist admission before returning.
// True means accepted, not processed; false means rejected (e.g. inbox full).
// Receivers coordinate shutdown with in-flight admission so accepted mail is
// preserved and closed receivers reject new mail.
type Deliver func(Message) bool

// Accept is a Deliver that explains a rejection. A nil error means accepted.
type Accept func(Message) error

var (
	// ErrNoRecipient means no registration holds the address.
	ErrNoRecipient = errors.New("no recipient holds the address")
	// ErrRejected is the generic rejection reported for a Deliver returning false.
	ErrRejected = errors.New("recipient rejected the message")
	// ErrInboxFull means the recipient is live but has too much unread mail.
	ErrInboxFull = errors.New("recipient inbox is full")
)

// A distinct registration lets a rejected delivery notice replacement while
// its callback was running, without comparing functions or holding mu.
type registration struct {
	accept Accept
}

type completionMail struct {
	messages []Message // includes the in-flight head, for pending deduplication
	draining bool
	woken    bool // WakeCompletions arrived during an in-flight admission
}

var (
	mu      sync.RWMutex
	routes  = map[string]*registration{}
	pending = map[string]*completionMail{}
)

// Register makes deliver the recipient for addr, replacing any prior recipient,
// and flushes retained completion mail in order. The returned cleanup removes
// only this registration, never a newer replacement. See Deliver for admission
// and shutdown coordination requirements.
func Register(addr string, deliver Deliver) func() {
	return RegisterAccept(addr, func(m Message) error {
		if deliver(m) {
			return nil
		}
		return ErrRejected
	})
}

// RegisterAccept is Register for a recipient that explains its rejections.
func RegisterAccept(addr string, accept Accept) func() {
	mu.Lock()
	route := &registration{accept: accept}
	routes[addr] = route
	mail := pending[addr]
	mu.Unlock()
	if mail != nil {
		flushCompletions(addr, mail, false)
	}
	return func() {
		mu.Lock()
		if routes[addr] == route {
			delete(routes, addr)
		}
		mu.Unlock()
	}
}

// Unregister removes addr's recipient. It is idempotent — unregistering an
// address no one holds is a no-op. An already-running delivery can still finish;
// a closed recipient must reject it so completion mail remains pending.
func Unregister(addr string) {
	mu.Lock()
	delete(routes, addr)
	mu.Unlock()
}

// Send routes m to the recipient registered for m.To, reporting whether it was
// delivered. It returns false when no one holds the address AND when the
// recipient is registered but drops the message (a full inbox) — so a caller
// that reports "delivered" only does so when the message was actually accepted.
// The delivery function runs outside the global broker lock.
func Send(m Message) bool {
	return SendErr(m) == nil
}

// SendErr is Send reporting why delivery failed: ErrNoRecipient, ErrRejected,
// or the recipient's own reason such as ErrInboxFull.
func SendErr(m Message) error {
	mu.RLock()
	route, ok := routes[m.To]
	mu.RUnlock()
	if !ok {
		return ErrNoRecipient
	}
	return route.accept(m)
}

// SendCompletion accepts structured completion mail for a non-Main owner.
// True means accepted for delivery or process-lifetime retention, not necessarily
// delivered. Absent/rejecting owners retain mail until Register or
// WakeCompletions; no fallback or auto-start occurs. Pending duplicates (owner,
// session, task, attempt) coalesce.
func SendCompletion(m Message) bool {
	if m.TaskCompletion == nil || m.To == "" || m.To == Main {
		return false
	}
	// Retained metadata belongs to the broker, not the caller or recipient.
	completion := *m.TaskCompletion
	m.TaskCompletion = &completion
	mu.Lock()
	mail := pending[m.To]
	if mail == nil {
		mail = &completionMail{}
		pending[m.To] = mail
	}
	for _, queued := range mail.messages {
		if queued.SessionID == m.SessionID && *queued.TaskCompletion == completion {
			mu.Unlock()
			return true
		}
	}
	mail.messages = append(mail.messages, m)
	mu.Unlock()
	flushCompletions(m.To, mail, false)
	return true
}

// WakeCompletions tells the broker that addr's inbox has room again. It retries
// only retained completion mail against the live registration: it never
// registers, starts an agent, or redelivers ordinary mail. Delivery may run
// synchronously. A wake during an in-flight admission is recorded instead, and
// the active drainer retries if that admission rejects.
func WakeCompletions(addr string) {
	mu.Lock()
	mail := pending[addr]
	mu.Unlock()
	if mail != nil {
		flushCompletions(addr, mail, true)
	}
}

// One drainer per owner preserves FIFO order, including across replacement
// registrations. Callbacks may reenter the broker; they never run under mu.
// A woken flush that finds a drainer leaves the wake for it to observe.
func flushCompletions(addr string, mail *completionMail, wake bool) {
	mu.Lock()
	if pending[addr] != mail || mail.draining {
		if wake && pending[addr] == mail {
			mail.woken = true
		}
		mu.Unlock()
		return
	}
	mail.draining = true
	for {
		route := routes[addr]
		if route == nil || len(mail.messages) == 0 {
			mail.draining = false
			if len(mail.messages) == 0 {
				delete(pending, addr)
			}
			mu.Unlock()
			return
		}
		// This admission observes every earlier wake. Only a new wake during
		// the callback warrants retrying a rejection by the same route.
		mail.woken = false
		m := mail.messages[0]
		completion := *m.TaskCompletion
		m.TaskCompletion = &completion
		mu.Unlock()
		accepted := route.accept(m) == nil
		mu.Lock()
		if pending[addr] != mail { // Reset detached this in-flight queue.
			mu.Unlock()
			return
		}
		if accepted {
			mail.messages[0] = Message{}
			mail.messages = mail.messages[1:]
		} else if routes[addr] == route && !mail.woken {
			mail.draining = false
			mu.Unlock()
			return
		}
		// If Register raced a rejection, retry against the new registration.
		// Its flush saw draining=true, so this drainer owns that retry.
	}
}

// Reset removes every registration and pending completion. Test-only.
func Reset() {
	mu.Lock()
	routes = map[string]*registration{}
	pending = map[string]*completionMail{}
	mu.Unlock()
}
