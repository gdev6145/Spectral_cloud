package store

import (
	"testing"
	"time"
)

func TestAuditRecordAndList(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		err := s.AuditRecord(AuditEntry{
			Tenant: "default",
			Method: "POST",
			Path:   "/blockchain/add",
			Status: 200,
		})
		if err != nil {
			t.Fatalf("AuditRecord: %v", err)
		}
	}

	entries, err := s.AuditList("default", 10)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(entries))
	}
}

func TestAuditListNewestFirst(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	for _, path := range []string{"/a", "/b", "/c"} {
		_ = s.AuditRecord(AuditEntry{Tenant: "t", Method: "POST", Path: path, Status: 200})
		time.Sleep(time.Millisecond)
	}

	entries, err := s.AuditList("t", 10)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// IDs should be descending (newest first).
	if entries[0].ID <= entries[1].ID {
		t.Errorf("expected descending IDs: %d %d", entries[0].ID, entries[1].ID)
	}
}

func TestAuditListTenantFilter(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	_ = s.AuditRecord(AuditEntry{Tenant: "alpha", Method: "POST", Path: "/x", Status: 200})
	_ = s.AuditRecord(AuditEntry{Tenant: "beta", Method: "DELETE", Path: "/y", Status: 204})
	_ = s.AuditRecord(AuditEntry{Tenant: "alpha", Method: "POST", Path: "/z", Status: 201})

	entries, err := s.AuditList("alpha", 10)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 alpha entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Tenant != "alpha" {
			t.Errorf("unexpected tenant: %s", e.Tenant)
		}
	}
}

func TestAuditListLimit(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	for i := 0; i < 20; i++ {
		_ = s.AuditRecord(AuditEntry{Tenant: "t", Method: "POST", Path: "/x", Status: 200})
	}

	entries, err := s.AuditList("t", 7)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if len(entries) != 7 {
		t.Errorf("expected 7 entries, got %d", len(entries))
	}
}

func TestAuditTimestampAutoSet(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	before := time.Now().UTC()
	_ = s.AuditRecord(AuditEntry{Tenant: "t", Method: "POST", Path: "/x", Status: 200})
	after := time.Now().UTC()

	entries, _ := s.AuditList("t", 1)
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	ts := entries[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not in range [%v, %v]", ts, before, after)
	}
}

func TestAuditListEmpty(t *testing.T) {
	s, cleanup := openAuditTestStore(t)
	defer cleanup()

	entries, err := s.AuditList("", 10)
	if err != nil {
		t.Fatalf("AuditList: %v", err)
	}
	if entries == nil {
		entries = []AuditEntry{}
	}
	if len(entries) != 0 {
		t.Errorf("expected empty list, got %d", len(entries))
	}
}

// openAuditTestStore creates a temporary BoltDB store for testing.
func openAuditTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(DBPath(dir))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s, func() { _ = s.Close() }
}
