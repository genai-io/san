// Package hub provides a central Hub for inter-agent communication.
// Producers call Publish(Event). The Hub routes each event to the target's
// registered delivery function. Consumers own their own buffers.
package hub

import (
	"sync"
	"time"
)

// Event is a CloudEvents-inspired message routed through the Hub.
type Event struct {
	Type    string // "task.completed", "agent.message", "cron.fired"
	Target  string // consumer: "agent:<id>", "main"
	Subject string // human-readable: "fix auth module completed"
	Data    string // payload: XML content, message text, etc.
	Time    time.Time
}

// Hub is pure pub/sub: map[string]func(Event). Three methods, routing only.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]func(Event)
}

func New() *Hub {
	return &Hub{subs: make(map[string]func(Event))}
}

// Register adds a delivery function for the given subscriber ID.
func (h *Hub) Register(id string, deliver func(Event)) {
	h.mu.Lock()
	h.subs[id] = deliver
	h.mu.Unlock()
}

// Unregister removes a subscriber's delivery function.
func (h *Hub) Unregister(id string) {
	h.mu.Lock()
	delete(h.subs, id)
	h.mu.Unlock()
}

// Publish routes an event to the target's delivery function.
func (h *Hub) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.RLock()
	deliver, ok := h.subs[e.Target]
	h.mu.RUnlock()
	if ok {
		deliver(e)
	}
}
