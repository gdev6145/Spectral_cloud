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

// ---------------------------------------------------------------------------
// Additional coverage: Load (default tenant), WriteBlocks, WriteRoutes,
// RotateEncryptedBackup
// ---------------------------------------------------------------------------

func TestLoad_DefaultTenant(t *testing.T) {
dir := t.TempDir()
s, err := Open(filepath.Join(dir, "test.db"))
if err != nil {
t.Fatalf("open: %v", err)
}
defer s.Close()

// Write blocks and routes to the default tenant, then Load into new objects.
chain := blockchain.NewBlockchain()
chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
if err := s.WriteBlocks(chain.Snapshot()); err != nil {
t.Fatalf("write blocks: %v", err)
}

router := routing.NewRoutingEngine()
_ = router.AddRoute("node-load", routing.RouteMetric{Latency: 42})
if err := s.WriteRoutes(router.ListRoutes()); err != nil {
t.Fatalf("write routes: %v", err)
}

chain2 := blockchain.NewBlockchain()
router2 := routing.NewRoutingEngine()
if err := s.Load(chain2, router2); err != nil {
t.Fatalf("load: %v", err)
}
if chain2.Height() != 2 {
t.Fatalf("expected height 2 after load, got %d", chain2.Height())
}
if router2.RouteCount() != 1 {
t.Fatalf("expected 1 route after load, got %d", router2.RouteCount())
}
}

func TestWriteBlocks_DefaultTenant(t *testing.T) {
dir := t.TempDir()
s, err := Open(filepath.Join(dir, "test.db"))
if err != nil {
t.Fatalf("open: %v", err)
}
defer s.Close()

chain := blockchain.NewBlockchain()
chain.AddBlock([]blockchain.Transaction{{Sender: "x", Recipient: "y", Amount: 7}})
if err := s.WriteBlocks(chain.Snapshot()); err != nil {
t.Fatalf("write blocks: %v", err)
}

blocks, err := s.ReadBlocks()
if err != nil {
t.Fatalf("read blocks: %v", err)
}
if len(blocks) != 2 {
t.Fatalf("expected 2 blocks, got %d", len(blocks))
}
}

func TestWriteRoutes_DefaultTenant(t *testing.T) {
dir := t.TempDir()
s, err := Open(filepath.Join(dir, "test.db"))
if err != nil {
t.Fatalf("open: %v", err)
}
defer s.Close()

router := routing.NewRoutingEngine()
_ = router.AddRoute("node-wr", routing.RouteMetric{Latency: 15, Throughput: 80})
if err := s.WriteRoutes(router.ListRoutes()); err != nil {
t.Fatalf("write routes: %v", err)
}

routes, err := s.ReadRoutes()
if err != nil {
t.Fatalf("read routes: %v", err)
}
if len(routes) != 1 || routes[0].Destination != "node-wr" {
t.Fatalf("unexpected routes: %v", routes)
}
}

func TestRotateEncryptedBackup(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

// Write data then close so BackupEncrypted can open it read-only.
func() {
s, err := Open(dbPath)
if err != nil {
t.Fatalf("open: %v", err)
}
defer s.Close()
chain := blockchain.NewBlockchain()
chain.AddBlock([]blockchain.Transaction{{Sender: "alice", Recipient: "bob", Amount: 100}})
_ = s.WriteBlocks(chain.Snapshot())
}()

// Generate two 32-byte keys.
oldKey := make([]byte, 32)
newKey := make([]byte, 32)
for i := range oldKey {
oldKey[i] = byte(i + 1)
}
for i := range newKey {
newKey[i] = byte(i + 100)
}
oldKeyB64 := base64.StdEncoding.EncodeToString(oldKey)
newKeyB64 := base64.StdEncoding.EncodeToString(newKey)

oldBackup := filepath.Join(dir, "backup.enc")
newBackup := filepath.Join(dir, "backup-new.enc")

if err := BackupEncrypted(dbPath, oldBackup, oldKeyB64); err != nil {
t.Fatalf("backup: %v", err)
}

	if err := RotateEncryptedBackup(oldBackup, newBackup, oldKeyB64, newKeyB64); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Restore from the rotated backup to verify decryption with new key.
	restoreDir := t.TempDir()
	restorePath := filepath.Join(restoreDir, "restored.db")
	if err := RestoreEncrypted(restorePath, newBackup, newKeyB64); err != nil {
		t.Fatalf("restore with new key: %v", err)
	}

	s2, err := Open(restorePath)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	defer s2.Close()

	blocks, err := s2.ReadBlocks()
	if err != nil {
		t.Fatalf("read restored blocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks after rotate, got %d", len(blocks))
	}
}
