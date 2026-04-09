package store

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(DBPath(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen(t *testing.T) {
	s := openTestStore(t)
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestPing(t *testing.T) {
	s := openTestStore(t)
	if err := s.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestDBPath(t *testing.T) {
	p := DBPath("/data")
	if p != filepath.Join("/data", "spectral.db") {
		t.Fatalf("unexpected path: %s", p)
	}
}

func TestHasData(t *testing.T) {
	s := openTestStore(t)

	has, err := s.HasData()
	if err != nil {
		t.Fatalf("HasData: %v", err)
	}
	if has {
		t.Fatal("expected no data on fresh store")
	}

	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	if err := s.SaveChain(chain); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	has, err = s.HasData()
	if err != nil {
		t.Fatalf("HasData after write: %v", err)
	}
	if !has {
		t.Fatal("expected data after saving chain")
	}
}

func TestWriteAndReadBlocks(t *testing.T) {
	s := openTestStore(t)

	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "alice", Recipient: "bob", Amount: 42}})

	if err := s.SaveChain(chain); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	blocks, err := s.ReadBlocks()
	if err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks, got none")
	}
}

func TestWriteAndReadRoutes(t *testing.T) {
	s := openTestStore(t)

	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})

	if err := s.SaveRoutes(router); err != nil {
		t.Fatalf("SaveRoutes: %v", err)
	}

	routes, err := s.ReadRoutes()
	if err != nil {
		t.Fatalf("ReadRoutes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected routes, got none")
	}
	if routes[0].Destination != "10.0.0.1:9000" {
		t.Fatalf("unexpected destination: %s", routes[0].Destination)
	}
}

func TestLoadTenant(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnsureTenant("acme"); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}

	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "x", Recipient: "y", Amount: 7}})
	if err := s.SaveChainTenant("acme", chain); err != nil {
		t.Fatalf("SaveChainTenant: %v", err)
	}

	chain2 := blockchain.NewBlockchain()
	router := routing.NewRoutingEngine()
	if err := s.LoadTenant("acme", chain2, router); err != nil {
		t.Fatalf("LoadTenant: %v", err)
	}
	if chain2.Height() == 0 {
		t.Fatal("expected blocks loaded for acme tenant")
	}
}

func TestTenantCRUD(t *testing.T) {
	s := openTestStore(t)

	// Create two tenants.
	for _, name := range []string{"tenant-a", "tenant-b"} {
		if err := s.EnsureTenant(name); err != nil {
			t.Fatalf("EnsureTenant(%s): %v", name, err)
		}
	}

	names, err := s.TenantNames()
	if err != nil {
		t.Fatalf("TenantNames: %v", err)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, want := range []string{"tenant-a", "tenant-b"} {
		if !found[want] {
			t.Fatalf("expected tenant %q in list", want)
		}
	}

	// Delete one.
	if err := s.DeleteTenant("tenant-a"); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	names, _ = s.TenantNames()
	for _, n := range names {
		if n == "tenant-a" {
			t.Fatal("tenant-a should be deleted")
		}
	}
}

func TestDeleteDefaultTenantFails(t *testing.T) {
	s := openTestStore(t)
	if err := s.DeleteTenant("default"); err == nil {
		t.Fatal("expected error deleting default tenant")
	}
}

func TestDeleteNonExistentTenantFails(t *testing.T) {
	s := openTestStore(t)
	if err := s.DeleteTenant("no-such-tenant"); err == nil {
		t.Fatal("expected error deleting non-existent tenant")
	}
}

func TestDeleteEmptyTenantFails(t *testing.T) {
	s := openTestStore(t)
	if err := s.DeleteTenant(""); err == nil {
		t.Fatal("expected error for empty tenant name")
	}
}

func TestEnsureTenantEmptyFails(t *testing.T) {
	s := openTestStore(t)
	if err := s.EnsureTenant(""); err == nil {
		t.Fatal("expected error for empty tenant name")
	}
}

func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	backupPath := filepath.Join(dir, "backup.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	_ = s.SaveChain(chain)
	_ = s.Close()

	if err := Backup(dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := Verify(backupPath); err != nil {
		t.Fatalf("Verify backup: %v", err)
	}

	// Restore over original.
	restorePath := filepath.Join(dir, "restored.db")
	if err := Restore(restorePath, backupPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := Verify(restorePath); err != nil {
		t.Fatalf("Verify restored: %v", err)
	}
}

func TestEncryptedBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "enc", Recipient: "dec", Amount: 99}})
	_ = s.SaveChain(chain)
	_ = s.Close()

	// 32-byte AES key.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)

	encPath := filepath.Join(dir, "backup.enc")
	if err := BackupEncrypted(dbPath, encPath, keyB64); err != nil {
		t.Fatalf("BackupEncrypted: %v", err)
	}
	if err := VerifyEncrypted(encPath, keyB64); err != nil {
		t.Fatalf("VerifyEncrypted: %v", err)
	}

	restorePath := filepath.Join(dir, "restored.db")
	if err := RestoreEncrypted(restorePath, encPath, keyB64); err != nil {
		t.Fatalf("RestoreEncrypted: %v", err)
	}
	if err := Verify(restorePath); err != nil {
		t.Fatalf("Verify after RestoreEncrypted: %v", err)
	}
}

func TestVerifyInvalidKeyLength(t *testing.T) {
	if err := BackupEncrypted("", "", base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestCompactInPlace(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	_ = s.SaveChain(chain)
	_ = s.Close()

	if err := CompactInPlace(dbPath); err != nil {
		t.Fatalf("CompactInPlace: %v", err)
	}
	if err := Verify(dbPath); err != nil {
		t.Fatalf("Verify after compact: %v", err)
	}
}

func TestCompactInPlaceNonExistent(t *testing.T) {
	if err := CompactInPlace("/tmp/does-not-exist-spectral.db"); err != nil {
		t.Fatalf("CompactInPlace on missing file should be no-op: %v", err)
	}
}

func TestWriteBlocksTenantAndReadBack(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnsureTenant("beta"); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}

	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "s1", Recipient: "r1", Amount: 10}})
	blocks := chain.Snapshot()

	if err := s.WriteBlocksTenant("beta", blocks); err != nil {
		t.Fatalf("WriteBlocksTenant: %v", err)
	}

	got, err := s.ReadBlocksTenant("beta")
	if err != nil {
		t.Fatalf("ReadBlocksTenant: %v", err)
	}
	if len(got) != len(blocks) {
		t.Fatalf("expected %d blocks, got %d", len(blocks), len(got))
	}
}

func TestWriteRoutesTenantAndReadBack(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnsureTenant("gamma"); err != nil {
		t.Fatalf("EnsureTenant: %v", err)
	}

	router := routing.NewRoutingEngine()
	router.AddRoute("192.168.1.1:8000", routing.RouteMetric{Latency: 10, Throughput: 50})
	routes := router.ListRoutes()

	if err := s.WriteRoutesTenant("gamma", routes); err != nil {
		t.Fatalf("WriteRoutesTenant: %v", err)
	}

	got, err := s.ReadRoutesTenant("gamma")
	if err != nil {
		t.Fatalf("ReadRoutesTenant: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected routes, got none")
	}
}
