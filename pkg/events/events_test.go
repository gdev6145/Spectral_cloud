package events

import (
	"testing"
	"time"
)

func TestPublishAndReceive(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe("s1", 8)
	b.Publish(Event{Type: EventBlockAdded, TenantID: "t1"})
	select {
	case ev := <-ch:
		if ev.Type != EventBlockAdded || ev.TenantID != "t1" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestPublishFillsTimestamp(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe("s1", 8)
	b.Publish(Event{Type: EventRouteAdded})
	ev := <-ch
	if ev.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be filled")
	}
}

func TestPublishFillsID(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe("s1", 8)
	b.Publish(Event{Type: EventRouteAdded})
	ev := <-ch
	if ev.ID == "" {
		t.Fatal("expected ID to be filled")
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := NewBroker()
	ch := b.Subscribe("s1", 8)
	b.Unsubscribe("s1")
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel not closed after unsubscribe")
	}
}

func TestSubscriberCount(t *testing.T) {
	b := NewBroker()
	if b.SubscriberCount() != 0 {
		t.Fatal("expected 0 initial subscribers")
	}
	b.Subscribe("s1", 8)
	b.Subscribe("s2", 8)
	if b.SubscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", b.SubscriberCount())
	}
	b.Unsubscribe("s1")
	if b.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber after unsubscribe, got %d", b.SubscriberCount())
	}
}

func TestSlowConsumerEventDropped(t *testing.T) {
	b := NewBroker()
	// Buffer of 1 — second event should be dropped.
	ch := b.Subscribe("s1", 1)
	b.Publish(Event{Type: EventBlockAdded})
	b.Publish(Event{Type: EventAgentRegistered}) // should be dropped
	ev := <-ch
	if ev.Type != EventBlockAdded {
		t.Fatalf("expected block_added, got %s", ev.Type)
	}
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra event: %s", extra.Type)
	default:
		// Correctly dropped.
	}
}

func TestMultipleSubscribers(t *testing.T) {
	b := NewBroker()
	ch1 := b.Subscribe("s1", 8)
	ch2 := b.Subscribe("s2", 8)
	b.Publish(Event{Type: EventMeshAnomaly})
	got1 := <-ch1
	got2 := <-ch2
	if got1.Type != EventMeshAnomaly || got2.Type != EventMeshAnomaly {
		t.Fatal("both subscribers should receive the event")
	}
}

func TestHistoryEmpty(t *testing.T) {
	b := NewBroker()
	if h := b.History(0); h != nil {
		t.Fatalf("expected nil history on fresh broker, got %v", h)
	}
}

func TestHistoryRetainsEvents(t *testing.T) {
	b := NewBrokerWithHistory(10)
	for i := 0; i < 5; i++ {
		b.Publish(Event{Type: EventBlockAdded})
	}
	h := b.History(0)
	if len(h) != 5 {
		t.Fatalf("expected 5 events in history, got %d", len(h))
	}
}

func TestHistoryRingWraps(t *testing.T) {
	b := NewBrokerWithHistory(3)
	// Publish 5 events into a ring of size 3.
	types := []EventType{EventBlockAdded, EventRouteAdded, EventRouteDeleted, EventAgentRegistered, EventAgentHeartbeat}
	for _, et := range types {
		b.Publish(Event{Type: et})
	}
	h := b.History(0)
	// Only the last 3 should be retained.
	if len(h) != 3 {
		t.Fatalf("expected 3 events (ring size), got %d", len(h))
	}
	// Newest last: agent_registered, agent_heartbeat.
	if h[2].Type != EventAgentHeartbeat {
		t.Fatalf("expected last event to be agent_heartbeat, got %s", h[2].Type)
	}
}

func TestHistoryLimitParam(t *testing.T) {
	b := NewBrokerWithHistory(20)
	for i := 0; i < 10; i++ {
		b.Publish(Event{Type: EventRouteAdded})
	}
	h := b.History(3)
	if len(h) != 3 {
		t.Fatalf("expected limit=3, got %d", len(h))
	}
}

func TestHistoryDisabled(t *testing.T) {
	b := NewBrokerWithHistory(0)
	b.Publish(Event{Type: EventBlockAdded})
	if h := b.History(0); h != nil {
		t.Fatalf("expected nil when history disabled, got %v", h)
	}
}

func TestHistoryOrderNewestLast(t *testing.T) {
	b := NewBrokerWithHistory(10)
	b.Publish(Event{Type: EventBlockAdded, TenantID: "first"})
	b.Publish(Event{Type: EventRouteAdded, TenantID: "second"})
	h := b.History(0)
	if len(h) != 2 {
		t.Fatalf("expected 2, got %d", len(h))
	}
	if h[0].TenantID != "first" || h[1].TenantID != "second" {
		t.Fatalf("expected oldest-first order, got %v %v", h[0].TenantID, h[1].TenantID)
	}
}
