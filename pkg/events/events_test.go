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
