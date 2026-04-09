package kv

import (
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()
	s.Set("t1", "foo", "bar", 0)
	e, ok := s.Get("t1", "foo")
	if !ok {
		t.Fatal("expected to find key")
	}
	if e.Value != "bar" || e.Key != "foo" || e.Tenant != "t1" {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestGetMissing(t *testing.T) {
	s := New()
	_, ok := s.Get("t1", "nope")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestGetExpired(t *testing.T) {
	s := New()
	s.Set("t1", "exp", "val", 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	_, ok := s.Get("t1", "exp")
	if ok {
		t.Fatal("expected expired key to be invisible")
	}
}

func TestDelete(t *testing.T) {
	s := New()
	s.Set("t1", "del", "v", 0)
	if !s.Delete("t1", "del") {
		t.Fatal("expected true")
	}
	if s.Delete("t1", "del") {
		t.Fatal("expected false on second delete")
	}
}

func TestList(t *testing.T) {
	s := New()
	s.Set("t1", "b", "2", 0)
	s.Set("t1", "a", "1", 0)
	s.Set("t1", "c-exp", "x", 1*time.Millisecond)
	s.Set("t2", "z", "other", 0) // different tenant
	time.Sleep(5 * time.Millisecond)

	entries := s.List("t1", "")
	if len(entries) != 2 {
		t.Fatalf("expected 2 live entries, got %d", len(entries))
	}
	if entries[0].Key != "a" || entries[1].Key != "b" {
		t.Errorf("expected sorted [a, b], got %v %v", entries[0].Key, entries[1].Key)
	}
}

func TestListPrefix(t *testing.T) {
	s := New()
	s.Set("t1", "cfg:a", "1", 0)
	s.Set("t1", "cfg:b", "2", 0)
	s.Set("t1", "other", "3", 0)

	entries := s.List("t1", "cfg:")
	if len(entries) != 2 {
		t.Errorf("expected 2, got %d", len(entries))
	}
}

func TestCount(t *testing.T) {
	s := New()
	s.Set("t1", "x", "1", 0)
	s.Set("t2", "y", "2", 0)
	s.Set("t1", "z-exp", "3", 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if n := s.Count(); n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestPrune(t *testing.T) {
	s := New()
	s.Set("t1", "live", "v", 0)
	s.Set("t1", "dead", "v", 1*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	n := s.Prune()
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}
	if s.Count() != 1 {
		t.Errorf("expected 1 live entry after prune")
	}
}
