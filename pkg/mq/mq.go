// Package mq provides an in-memory, multi-tenant, topic-based message queue.
// Messages are published to named topics and consumed (dequeued) by callers.
// Each tenant has its own isolated topic namespace.
package mq

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Message is a single queued item.
type Message struct {
	ID        string         `json:"id"`
	Topic     string         `json:"topic"`
	Tenant    string         `json:"tenant"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// TopicInfo summarises a topic without returning message bodies.
type TopicInfo struct {
	Topic   string `json:"topic"`
	Tenant  string `json:"tenant"`
	Pending int    `json:"pending"`
}

// Queue is a thread-safe multi-tenant topic-based message queue.
type Queue struct {
	mu      sync.Mutex
	topics  map[string]map[string][]*Message // tenant → topic → messages (FIFO)
	counter uint64
}

// New creates an empty Queue.
func New() *Queue {
	return &Queue{topics: make(map[string]map[string][]*Message)}
}

func (q *Queue) tenantTopics(tenant string) map[string][]*Message {
	if q.topics[tenant] == nil {
		q.topics[tenant] = make(map[string][]*Message)
	}
	return q.topics[tenant]
}

// Publish appends a message to the named topic for tenant.
// Returns the newly created Message.
func (q *Queue) Publish(tenant, topic string, payload map[string]any) (*Message, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	id := fmt.Sprintf("msg-%d", atomic.AddUint64(&q.counter, 1))
	msg := &Message{
		ID:        id,
		Topic:     topic,
		Tenant:    tenant,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	q.mu.Lock()
	tt := q.tenantTopics(tenant)
	tt[topic] = append(tt[topic], msg)
	q.mu.Unlock()
	return msg, nil
}

// Consume dequeues up to count messages from topic for tenant (FIFO).
// If count <= 0, all pending messages are returned and the topic is cleared.
func (q *Queue) Consume(tenant, topic string, count int) []Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	tt := q.tenantTopics(tenant)
	msgs := tt[topic]
	if len(msgs) == 0 {
		return []Message{}
	}
	if count <= 0 || count >= len(msgs) {
		out := make([]Message, len(msgs))
		for i, m := range msgs {
			out[i] = *m
		}
		tt[topic] = nil
		return out
	}
	out := make([]Message, count)
	for i, m := range msgs[:count] {
		out[i] = *m
	}
	tt[topic] = msgs[count:]
	return out
}

// Peek returns the next message in topic without removing it.
// Returns (Message{}, false) when the topic is empty.
func (q *Queue) Peek(tenant, topic string) (Message, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	tt := q.tenantTopics(tenant)
	msgs := tt[topic]
	if len(msgs) == 0 {
		return Message{}, false
	}
	return *msgs[0], true
}

// Topics returns info about all topics with at least one pending message for
// the given tenant. Pass "" to list across all tenants.
func (q *Queue) Topics(tenant string) []TopicInfo {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []TopicInfo
	for t, tt := range q.topics {
		if tenant != "" && t != tenant {
			continue
		}
		for topic, msgs := range tt {
			if len(msgs) == 0 {
				continue
			}
			out = append(out, TopicInfo{Topic: topic, Tenant: t, Pending: len(msgs)})
		}
	}
	return out
}

// Count returns the number of pending messages in a topic.
func (q *Queue) Count(tenant, topic string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	tt := q.tenantTopics(tenant)
	return len(tt[topic])
}

// Purge removes all messages from a topic. Returns the number removed.
func (q *Queue) Purge(tenant, topic string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	tt := q.tenantTopics(tenant)
	n := len(tt[topic])
	tt[topic] = nil
	return n
}
