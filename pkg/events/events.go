// Package events provides a lightweight in-process event broker used to fan
// out structured events to subscribers (e.g. SSE connections, webhook hooks).
package events

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// EventType identifies the kind of event that occurred.
type EventType string

const (
	EventBlockAdded        EventType = "block_added"
	EventRouteAdded        EventType = "route_added"
	EventRouteDeleted      EventType = "route_deleted"
	EventAgentRegistered   EventType = "agent_registered"
	EventAgentDeregistered EventType = "agent_deregistered"
	EventAgentHeartbeat    EventType = "agent_heartbeat"
	EventMeshAnomaly       EventType = "mesh_anomaly"
)

// Event is the canonical event envelope published through the broker.
type Event struct {
	// ID is a random hex string that uniquely identifies this event.
	ID        string      `json:"id"`
	Type      EventType   `json:"type"`
	TenantID  string      `json:"tenant_id,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// Broker fans out published events to all active subscribers. Slow subscribers
// have events dropped (non-blocking send) rather than backpressuring publishers.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event
}

// NewBroker creates an empty broker.
func NewBroker() *Broker {
	return &Broker{subscribers: make(map[string]chan Event)}
}

// Subscribe registers a channel for events and returns it. The caller must call
// Unsubscribe when done to avoid leaking the channel. If bufSize <= 0 it
// defaults to 64.
func (b *Broker) Subscribe(id string, bufSize int) <-chan Event {
	if bufSize <= 0 {
		bufSize = 64
	}
	ch := make(chan Event, bufSize)
	b.mu.Lock()
	b.subscribers[id] = ch
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes the subscriber channel for id.
func (b *Broker) Unsubscribe(id string) {
	b.mu.Lock()
	if ch, ok := b.subscribers[id]; ok {
		close(ch)
		delete(b.subscribers, id)
	}
	b.mu.Unlock()
}

// Publish sends an event to all current subscribers. Fields left zero are
// filled in automatically. Slow consumers are skipped to keep publishing fast.
func (b *Broker) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ID == "" {
		var raw [8]byte
		_, _ = rand.Read(raw[:])
		event.ID = hex.EncodeToString(raw[:])
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
			// Slow consumer — drop rather than block.
		}
	}
}

// SubscriberCount returns the number of active subscribers.
func (b *Broker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
