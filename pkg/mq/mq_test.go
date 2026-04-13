package mq

import (
	"testing"
)

func TestPublishAndConsume(t *testing.T) {
	q := New()
	tenant := "tenant1"
	topic := "events"

	msg, err := q.Publish(tenant, topic, map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("expected non-empty message ID")
	}
	if msg.Topic != topic {
		t.Fatalf("expected topic %q, got %q", topic, msg.Topic)
	}
	if msg.Tenant != tenant {
		t.Fatalf("expected tenant %q, got %q", tenant, msg.Tenant)
	}
	if q.Count(tenant, topic) != 1 {
		t.Fatalf("expected count 1, got %d", q.Count(tenant, topic))
	}

	msgs := q.Consume(tenant, topic, 1)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != msg.ID {
		t.Fatalf("wrong message ID: %q vs %q", msgs[0].ID, msg.ID)
	}
	if q.Count(tenant, topic) != 0 {
		t.Fatalf("expected count 0 after consume, got %d", q.Count(tenant, topic))
	}
}

func TestConsumePartial(t *testing.T) {
	q := New()
	tenant := "t1"
	topic := "jobs"

	for i := 0; i < 5; i++ {
		if _, err := q.Publish(tenant, topic, map[string]any{"i": i}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if q.Count(tenant, topic) != 5 {
		t.Fatalf("expected 5 pending, got %d", q.Count(tenant, topic))
	}

	msgs := q.Consume(tenant, topic, 3)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if q.Count(tenant, topic) != 2 {
		t.Fatalf("expected 2 remaining, got %d", q.Count(tenant, topic))
	}
}

func TestConsumeAll(t *testing.T) {
	q := New()
	for i := 0; i < 4; i++ {
		q.Publish("t", "top", nil) //nolint:errcheck
	}
	msgs := q.Consume("t", "top", 0)
	if len(msgs) != 4 {
		t.Fatalf("expected 4, got %d", len(msgs))
	}
	if q.Count("t", "top") != 0 {
		t.Fatalf("expected 0 remaining")
	}
}

func TestPurge(t *testing.T) {
	q := New()
	for i := 0; i < 3; i++ {
		q.Publish("t", "top", nil) //nolint:errcheck
	}
	n := q.Purge("t", "top")
	if n != 3 {
		t.Fatalf("expected 3 purged, got %d", n)
	}
	if q.Count("t", "top") != 0 {
		t.Fatal("expected empty after purge")
	}
}

func TestTenantIsolation(t *testing.T) {
	q := New()
	q.Publish("tenant-a", "events", map[string]any{"from": "a"}) //nolint:errcheck
	q.Publish("tenant-b", "events", map[string]any{"from": "b"}) //nolint:errcheck

	msgA := q.Consume("tenant-a", "events", 10)
	if len(msgA) != 1 || msgA[0].Payload["from"] != "a" {
		t.Fatalf("unexpected messages for tenant-a: %+v", msgA)
	}
	msgB := q.Consume("tenant-b", "events", 10)
	if len(msgB) != 1 || msgB[0].Payload["from"] != "b" {
		t.Fatalf("unexpected messages for tenant-b: %+v", msgB)
	}
}

func TestTopics(t *testing.T) {
	q := New()
	q.Publish("t", "alpha", nil)   //nolint:errcheck
	q.Publish("t", "alpha", nil)   //nolint:errcheck
	q.Publish("t", "beta", nil)    //nolint:errcheck

	topics := q.Topics("t")
	counts := map[string]int{}
	for _, ti := range topics {
		counts[ti.Topic] = ti.Pending
	}
	if counts["alpha"] != 2 {
		t.Fatalf("expected alpha=2, got %d", counts["alpha"])
	}
	if counts["beta"] != 1 {
		t.Fatalf("expected beta=1, got %d", counts["beta"])
	}
}

func TestPublishRequiresTenant(t *testing.T) {
	q := New()
	_, err := q.Publish("", "topic", nil)
	if err == nil {
		t.Fatal("expected error for empty tenant")
	}
}

func TestPublishRequiresTopic(t *testing.T) {
	q := New()
	_, err := q.Publish("t", "", nil)
	if err == nil {
		t.Fatal("expected error for empty topic")
	}
}
