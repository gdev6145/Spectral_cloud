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

// DefaultHistorySize is how many events the broker retains by default.
const DefaultHistorySize = 256

// Broker fans out published events to all active subscribers. Slow subscribers
// have events dropped (non-blocking send) rather than backpressuring publishers.
// It also maintains a bounded history ring-buffer so late-joiners can catch up.
type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event

	histMu     sync.RWMutex
	histBuf    []Event
	histHead   int // next write index (ring)
	histMax    int
	histCount  int // total events published (not ring size)
}

// NewBroker creates an empty broker with the default history buffer size.
func NewBroker() *Broker {
	return NewBrokerWithHistory(DefaultHistorySize)
}

// NewBrokerWithHistory creates a broker whose history ring holds at most
// historySize events. historySize <= 0 disables history retention.
func NewBrokerWithHistory(historySize int) *Broker {
	if historySize < 0 {
		historySize = 0
	}
	b := &Broker{
		subscribers: make(map[string]chan Event),
		histMax:     historySize,
	}
	if historySize > 0 {
		b.histBuf = make([]Event, historySize)
	}
	return b
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
// The event is also appended to the internal history ring-buffer.
func (b *Broker) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ID == "" {
		var raw [8]byte
		_, _ = rand.Read(raw[:])
		event.ID = hex.EncodeToString(raw[:])
	}
	// Append to ring buffer.
	if b.histMax > 0 {
		b.histMu.Lock()
		b.histBuf[b.histHead] = event
		b.histHead = (b.histHead + 1) % b.histMax
		b.histCount++
		b.histMu.Unlock()
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

// History returns the most recent events, newest last. limit <= 0 returns all
// retained events (up to the ring capacity). Returns nil when history is
// disabled or empty.
func (b *Broker) History(limit int) []Event {
	if b.histMax == 0 {
		return nil
	}
	b.histMu.RLock()
	defer b.histMu.RUnlock()
	total := b.histCount
	if total == 0 {
		return nil
	}
	// Number of valid entries in the ring.
	filled := total
	if filled > b.histMax {
		filled = b.histMax
	}
	if limit <= 0 || limit > filled {
		limit = filled
	}
	out := make([]Event, limit)
	// Walk backwards from the most recently written slot.
	for i := 0; i < limit; i++ {
		// histHead points to the next write slot, so head-1 is the newest.
		idx := (b.histHead - 1 - i + b.histMax) % b.histMax
		out[limit-1-i] = b.histBuf[idx]
	}
	return out
}

// SubscriberCount returns the number of active subscribers.
func (b *Broker) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
