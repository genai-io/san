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
// rejecting owner until it registers again. Retention is process-local; there
// are no topics, broadcasts, fallback recipients, or agent startup side effects.
// What delivered mail does is the recipient's business.
package broker

import "sync"

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

// Deliver hands a routed message to a recipient. It must not block; it returns
// whether the message was accepted — false means the recipient was reachable
// but dropped it (e.g. its inbox was full), so the sender can tell "delivered"
// apart from "silently discarded".
type Deliver func(Message) bool

// A distinct registration lets a rejected delivery notice replacement while
// its callback was running, without comparing functions or holding mu.
type registration struct {
	deliver Deliver
}

type completionMail struct {
	messages []Message // includes the in-flight head, for pending deduplication
	draining bool
}

var (
	mu      sync.RWMutex
	routes  = map[string]*registration{}
	pending = map[string]*completionMail{}
)

// Register makes deliver the recipient for addr, replacing any prior recipient,
// and flushes retained completion mail in order. A rejection leaves the remaining
// mail pending for a later registration. Deliver must not block.
func Register(addr string, deliver Deliver) {
	mu.Lock()
	routes[addr] = &registration{deliver: deliver}
	mail := pending[addr]
	mu.Unlock()
	if mail != nil {
		flushCompletions(addr, mail)
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
// The delivery function runs outside the lock and must not block.
func Send(m Message) bool {
	mu.RLock()
	deliver, ok := routes[m.To]
	mu.RUnlock()
	if !ok {
		return false
	}
	return deliver.deliver(m)
}

// SendCompletion accepts structured completion mail for a non-Main owner.
// True means accepted for delivery or process-lifetime retention, not necessarily
// delivered. Absent/rejecting owners retain mail until Register; no fallback or
// auto-start occurs. Pending duplicates (owner, session, task, attempt) coalesce.
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
	flushCompletions(m.To, mail)
	return true
}

// One drainer per owner preserves FIFO order, including across replacement
// registrations. Callbacks may reenter the broker; they never run under mu.
func flushCompletions(addr string, mail *completionMail) {
	mu.Lock()
	if pending[addr] != mail || mail.draining {
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
		m := mail.messages[0]
		completion := *m.TaskCompletion
		m.TaskCompletion = &completion
		mu.Unlock()
		accepted := route.deliver(m)
		mu.Lock()
		if pending[addr] != mail { // Reset detached this in-flight queue.
			mu.Unlock()
			return
		}
		if accepted {
			mail.messages[0] = Message{}
			mail.messages = mail.messages[1:]
		} else if routes[addr] == route {
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
