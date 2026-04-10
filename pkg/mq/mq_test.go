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
	for i := 0; i < 5; i++ {
		_, err := q.Publish("t", "topic", nil)
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	got := q.Consume("t", "topic", 3)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	if q.Count("t", "topic") != 2 {
		t.Fatalf("expected 2 remaining, got %d", q.Count("t", "topic"))
	}
}

func TestConsumeEmpty(t *testing.T) {
	q := New()
	msgs := q.Consume("t", "empty", 10)
	if len(msgs) != 0 {
		t.Fatalf("expected 0, got %d", len(msgs))
	}
}

func TestPeek(t *testing.T) {
	q := New()
	q.Publish("t", "peek-topic", map[string]any{"n": 1}) //nolint:errcheck
	msg, ok := q.Peek("t", "peek-topic")
	if !ok {
		t.Fatal("expected peek to return true")
	}
	// Peek should not remove the message.
	if q.Count("t", "peek-topic") != 1 {
		t.Fatal("peek must not consume message")
	}
	_ = msg
}

func TestTopics(t *testing.T) {
	q := New()
	q.Publish("ta", "alpha", nil) //nolint:errcheck
	q.Publish("ta", "beta", nil)  //nolint:errcheck
	q.Publish("tb", "alpha", nil) //nolint:errcheck

	all := q.Topics("")
	if len(all) != 3 {
		t.Fatalf("expected 3 topic entries, got %d", len(all))
	}

	forTA := q.Topics("ta")
	if len(forTA) != 2 {
		t.Fatalf("expected 2 topics for ta, got %d", len(forTA))
	}
}

func TestPurge(t *testing.T) {
	q := New()
	q.Publish("t", "purge-topic", nil) //nolint:errcheck
	q.Publish("t", "purge-topic", nil) //nolint:errcheck

	removed := q.Purge("t", "purge-topic")
	if removed != 2 {
		t.Fatalf("expected 2 purged, got %d", removed)
	}
	if q.Count("t", "purge-topic") != 0 {
		t.Fatal("expected empty topic after purge")
	}
}

func TestPublishValidation(t *testing.T) {
	q := New()
	_, err := q.Publish("", "topic", nil)
	if err == nil {
		t.Fatal("expected error for empty tenant")
	}
	_, err = q.Publish("tenant", "", nil)
	if err == nil {
		t.Fatal("expected error for empty topic")
	}
}

func TestTenantIsolation(t *testing.T) {
	q := New()
	q.Publish("a", "shared", map[string]any{"from": "a"}) //nolint:errcheck
	q.Publish("b", "shared", map[string]any{"from": "b"}) //nolint:errcheck

	msgs := q.Consume("a", "shared", 10)
	if len(msgs) != 1 || msgs[0].Payload["from"] != "a" {
		t.Fatal("tenant isolation broken for tenant a")
	}
	msgs = q.Consume("b", "shared", 10)
	if len(msgs) != 1 || msgs[0].Payload["from"] != "b" {
		t.Fatal("tenant isolation broken for tenant b")
	}
}

func TestConsumeAll(t *testing.T) {
	q := New()
	for i := 0; i < 4; i++ {
		q.Publish("t", "all", nil) //nolint:errcheck
	}
	// count <= 0 should consume all
	msgs := q.Consume("t", "all", 0)
	if len(msgs) != 4 {
		t.Fatalf("expected 4, got %d", len(msgs))
	}
	if q.Count("t", "all") != 0 {
		t.Fatal("expected empty after consume all")
	}
}
