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
// That is the whole broker — a map guarded by a mutex, with direct addressing:
// no topics, no broadcast, no queuing beyond the recipient's own inbox. What a
// delivered message does (land in a subagent's inbox, wake the main loop) is
// the recipient's business, kept out of here so the broker only routes.
package broker

import "sync"

// Main is the well-known address of the main conversation.
const Main = "main"

// Message is one routed message. From/To are agent addresses; Subject is a
// short human-facing notice (may be empty); Content is the body delivered to
// the recipient.
type Message struct {
	From    string
	To      string
	Subject string
	Content string
}

var (
	mu     sync.RWMutex
	routes = map[string]func(Message){}
)

// Register makes deliver the recipient for addr. Addresses are unique per run
// (a task id, or Main), so an address is registered by exactly one agent at a
// time.
func Register(addr string, deliver func(Message)) {
	mu.Lock()
	routes[addr] = deliver
	mu.Unlock()
}

// Unregister removes addr's recipient. It is idempotent — unregistering an
// address no one holds is a no-op.
func Unregister(addr string) {
	mu.Lock()
	delete(routes, addr)
	mu.Unlock()
}

// Send routes m to the recipient registered for m.To, reporting whether it was
// delivered. A message to an unregistered address is dropped. The delivery
// function runs outside the lock and must not block (it enqueues and returns).
func Send(m Message) bool {
	mu.RLock()
	deliver, ok := routes[m.To]
	mu.RUnlock()
	if !ok {
		return false
	}
	deliver(m)
	return true
}

// Reset removes every registration. Test-only.
func Reset() {
	mu.Lock()
	routes = map[string]func(Message){}
	mu.Unlock()
}
