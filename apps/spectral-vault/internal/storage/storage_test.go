package storage

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPutGet(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	content := "hello vault"
	n, err := s.Put("tenant1", "file1.txt", strings.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("Put: expected %d bytes, got %d", len(content), n)
	}

	rc, err := s.Get("tenant1", "file1.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != content {
		t.Fatalf("Get: expected %q, got %q", content, string(data))
	}
}

func TestDeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Delete non-existent file should return an error.
	err = s.Delete("tenant1", "nonexistent.txt")
	if err == nil {
		t.Fatal("Delete: expected error for non-existent file, got nil")
	}

	// Get non-existent file should return an error.
	_, err = s.Get("tenant1", "nonexistent.txt")
	if err == nil {
		t.Fatal("Get: expected error for non-existent file, got nil")
	}

	// Put then Delete should succeed.
	_, err = s.Put("tenant1", "to_delete.txt", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err = s.Delete("tenant1", "to_delete.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Confirm file is gone on disk.
	path := s.StoragePath("tenant1", "to_delete.txt")
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat returned: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = s.Put("tenantA", "shared_key.txt", strings.NewReader("tenant A data"))
	if err != nil {
		t.Fatalf("Put tenantA: %v", err)
	}
	_, err = s.Put("tenantB", "shared_key.txt", strings.NewReader("tenant B data"))
	if err != nil {
		t.Fatalf("Put tenantB: %v", err)
	}

	readTenant := func(tenant string) string {
		rc, err := s.Get(tenant, "shared_key.txt")
		if err != nil {
			t.Fatalf("Get %s: %v", tenant, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll %s: %v", tenant, err)
		}
		return string(data)
	}

	if got := readTenant("tenantA"); got != "tenant A data" {
		t.Fatalf("tenantA: expected %q, got %q", "tenant A data", got)
	}
	if got := readTenant("tenantB"); got != "tenant B data" {
		t.Fatalf("tenantB: expected %q, got %q", "tenant B data", got)
	}
}
