package circuit

import (
	"testing"
	"time"
)

func TestClosedAllows(t *testing.T) {
	m := New(3, 10*time.Second)
	if !m.Allow("a1") {
		t.Fatal("closed circuit should allow")
	}
}

func TestOpensAfterThreshold(t *testing.T) {
	m := New(3, 10*time.Second)
	m.RecordFailure("a1")
	m.RecordFailure("a1")
	if m.Allow("a1") != true {
		t.Error("should still allow at 2 failures (threshold=3)")
	}
	m.RecordFailure("a1")
	if m.Allow("a1") {
		t.Error("should block after 3 failures")
	}
	b := m.Get("a1")
	if b.State != StateOpen {
		t.Errorf("expected open, got %s", b.State)
	}
}

func TestHalfOpenAfterTimeout(t *testing.T) {
	m := New(1, 20*time.Millisecond)
	m.RecordFailure("a1")
	if m.Allow("a1") {
		t.Error("should block immediately after open")
	}
	time.Sleep(30 * time.Millisecond)
	if !m.Allow("a1") {
		t.Error("should allow probe after reset timeout")
	}
	if m.Get("a1").State != StateHalfOpen {
		t.Error("expected half_open after timeout probe")
	}
}

func TestHalfOpenSuccessCloses(t *testing.T) {
	m := New(1, 10*time.Millisecond)
	m.RecordFailure("a1")
	time.Sleep(15 * time.Millisecond)
	m.Allow("a1") // transitions to half_open
	m.RecordSuccess("a1")
	if m.Get("a1").State != StateClosed {
		t.Error("expected closed after success in half_open")
	}
}

func TestHalfOpenFailureReopens(t *testing.T) {
	m := New(1, 10*time.Millisecond)
	m.RecordFailure("a1")
	time.Sleep(15 * time.Millisecond)
	m.Allow("a1") // transitions to half_open
	m.RecordFailure("a1")
	if m.Get("a1").State != StateOpen {
		t.Error("expected re-open after failure in half_open")
	}
}

func TestReset(t *testing.T) {
	m := New(1, 10*time.Second)
	m.RecordFailure("a1")
	m.Reset("a1")
	b := m.Get("a1")
	if b.State != StateClosed || b.Failures != 0 {
		t.Errorf("expected closed/0 after reset, got %s/%d", b.State, b.Failures)
	}
	if !m.Allow("a1") {
		t.Error("should allow after reset")
	}
}

func TestRecordSuccess(t *testing.T) {
	m := New(3, 10*time.Second)
	m.RecordFailure("a1")
	m.RecordFailure("a1")
	m.RecordSuccess("a1")
	b := m.Get("a1")
	if b.Failures != 0 {
		t.Errorf("expected failures reset to 0, got %d", b.Failures)
	}
}

func TestList(t *testing.T) {
	m := New(3, 10*time.Second)
	m.RecordFailure("a1")
	m.RecordFailure("b2")
	list := m.List()
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestDelete(t *testing.T) {
	m := New(3, 10*time.Second)
	m.RecordFailure("a1")
	m.Delete("a1")
	list := m.List()
	if len(list) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(list))
	}
}
