package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	bolt "go.etcd.io/bbolt"
)

const (
	dbVersion = 1

	bucketMeta    = "meta"
	bucketData    = "data"
	bucketTenants = "tenants"

	keyVersion = "version"
	keyBlocks  = "blocks"
	keyRoutes  = "routes"
)

const defaultTenant = "default"

var errFound = errors.New("found")

type Store struct {
	db *bolt.DB
}

func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Ping() error {
	if s == nil || s.db == nil {
		return errors.New("store not initialized")
	}
	return s.db.View(func(tx *bolt.Tx) error {
		return nil
	})
}

func (s *Store) init() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketData)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketTenants)); err != nil {
			return err
		}
		current := meta.Get([]byte(keyVersion))
		if current == nil {
			return meta.Put([]byte(keyVersion), []byte{byte(dbVersion)})
		}
		if int(current[0]) > dbVersion {
			return errors.New("unsupported db version")
		}
		return nil
	})
}

func (s *Store) HasData() (bool, error) {
	var has bool
	err := s.db.View(func(tx *bolt.Tx) error {
		tenants := tx.Bucket([]byte(bucketTenants))
		if tenants != nil {
			return tenants.ForEach(func(k, v []byte) error {
				if v != nil {
					return nil
				}
				t := tenants.Bucket(k)
				if t == nil {
					return nil
				}
				if t.Get([]byte(keyBlocks)) != nil || t.Get([]byte(keyRoutes)) != nil {
					has = true
					return errFound
				}
				return nil
			})
		}
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		if data.Get([]byte(keyBlocks)) != nil || data.Get([]byte(keyRoutes)) != nil {
			has = true
		}
		return nil
	})
	if err != nil && errors.Is(err, errFound) {
		return true, nil
	}
	return has, err
}

func (s *Store) Load(chain *blockchain.Blockchain, router *routing.RoutingEngine) error {
	return s.LoadTenant(defaultTenant, chain, router)
}

func (s *Store) ReadBlocks() ([]blockchain.Block, error) {
	return s.ReadBlocksTenant(defaultTenant)
}

func (s *Store) ReadRoutes() ([]routing.Route, error) {
	return s.ReadRoutesTenant(defaultTenant)
}

func (s *Store) WriteBlocks(blocks []blockchain.Block) error {
	return s.WriteBlocksTenant(defaultTenant, blocks)
}

func (s *Store) WriteRoutes(routes []routing.Route) error {
	return s.WriteRoutesTenant(defaultTenant, routes)
}

func (s *Store) SaveChain(chain *blockchain.Blockchain) error {
	return s.SaveChainTenant(defaultTenant, chain)
}

func (s *Store) SaveRoutes(router *routing.RoutingEngine) error {
	return s.SaveRoutesTenant(defaultTenant, router)
}

func (s *Store) EnsureTenant(tenant string) error {
	if strings.TrimSpace(tenant) == "" {
		return errors.New("tenant is empty")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		tenants := tx.Bucket([]byte(bucketTenants))
		if tenants == nil {
			return errors.New("tenants bucket missing")
		}
		_, err := tenants.CreateBucketIfNotExists([]byte(tenant))
		return err
	})
}

// DeleteTenant removes the tenant's sub-bucket and all its data. The built-in
// "default" tenant cannot be deleted.
func (s *Store) DeleteTenant(tenant string) error {
	if strings.TrimSpace(tenant) == "" {
		return errors.New("tenant is empty")
	}
	if tenant == defaultTenant {
		return errors.New("cannot delete the default tenant")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		tenants := tx.Bucket([]byte(bucketTenants))
		if tenants == nil {
			return errors.New("tenants bucket missing")
		}
		if tenants.Bucket([]byte(tenant)) == nil {
			return errors.New("tenant not found")
		}
		return tenants.DeleteBucket([]byte(tenant))
	})
}

func (s *Store) TenantNames() ([]string, error) {
	var out []string
	err := s.db.View(func(tx *bolt.Tx) error {
		tenants := tx.Bucket([]byte(bucketTenants))
		if tenants == nil {
			return nil
		}
		return tenants.ForEach(func(k, v []byte) error {
			if v != nil {
				return nil
			}
			out = append(out, string(k))
			return nil
		})
	})
	return out, err
}

func (s *Store) LoadTenant(tenant string, chain *blockchain.Blockchain, router *routing.RoutingEngine) error {
	if strings.TrimSpace(tenant) == "" {
		return errors.New("tenant is empty")
	}
	return s.db.View(func(tx *bolt.Tx) error {
		if raw := readTenantKey(tx, tenant, keyBlocks); raw != nil {
			var blocks []blockchain.Block
			if err := json.Unmarshal(raw, &blocks); err != nil {
				return err
			}
			chain.Load(blocks)
		}
		if raw := readTenantKey(tx, tenant, keyRoutes); raw != nil {
			var routes []routing.Route
			if err := json.Unmarshal(raw, &routes); err != nil {
				return err
			}
			router.Load(routes)
		}
		return nil
	})
}

func (s *Store) ReadBlocksTenant(tenant string) ([]blockchain.Block, error) {
	var blocks []blockchain.Block
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := readTenantKey(tx, tenant, keyBlocks)
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &blocks)
	})
	return blocks, err
}

func (s *Store) ReadRoutesTenant(tenant string) ([]routing.Route, error) {
	var routes []routing.Route
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := readTenantKey(tx, tenant, keyRoutes)
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &routes)
	})
	return routes, err
}

func (s *Store) WriteBlocksTenant(tenant string, blocks []blockchain.Block) error {
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tenantBucket(tx, tenant, true)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(keyBlocks), data); err != nil {
			return err
		}
		return cleanupLegacyDefault(tx, tenant, keyBlocks)
	})
}

func (s *Store) WriteRoutesTenant(tenant string, routes []routing.Route) error {
	data, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tenantBucket(tx, tenant, true)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(keyRoutes), data); err != nil {
			return err
		}
		return cleanupLegacyDefault(tx, tenant, keyRoutes)
	})
}

func (s *Store) SaveChainTenant(tenant string, chain *blockchain.Blockchain) error {
	blocks := chain.Snapshot()
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tenantBucket(tx, tenant, true)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(keyBlocks), data); err != nil {
			return err
		}
		return cleanupLegacyDefault(tx, tenant, keyBlocks)
	})
}

func (s *Store) SaveRoutesTenant(tenant string, router *routing.RoutingEngine) error {
	routes := router.ListRoutes()
	data, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tenantBucket(tx, tenant, true)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(keyRoutes), data); err != nil {
			return err
		}
		return cleanupLegacyDefault(tx, tenant, keyRoutes)
	})
}

func readTenantKey(tx *bolt.Tx, tenant, key string) []byte {
	if strings.TrimSpace(tenant) == "" {
		return nil
	}
	t, _ := tenantBucket(tx, tenant, false)
	if t != nil {
		if raw := t.Get([]byte(key)); raw != nil {
			return raw
		}
	}
	if tenant == defaultTenant {
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		return data.Get([]byte(key))
	}
	return nil
}

func tenantBucket(tx *bolt.Tx, tenant string, create bool) (*bolt.Bucket, error) {
	tenants := tx.Bucket([]byte(bucketTenants))
	if tenants == nil {
		return nil, errors.New("tenants bucket missing")
	}
	if create {
		return tenants.CreateBucketIfNotExists([]byte(tenant))
	}
	return tenants.Bucket([]byte(tenant)), nil
}

func cleanupLegacyDefault(tx *bolt.Tx, tenant, key string) error {
	if tenant != defaultTenant {
		return nil
	}
	data := tx.Bucket([]byte(bucketData))
	if data == nil {
		return nil
	}
	return data.Delete([]byte(key))
}

// PutKV stores an arbitrary value under a prefixed key in the tenant's bucket.
// The key is stored as-is; callers should use a consistent prefix (e.g. "job_", "sched_").
func (s *Store) PutKV(tenant, key string, value []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tenantBucket(tx, tenant, true)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), value)
	})
}

// GetKV retrieves the value for the given key from the tenant's bucket.
// Returns (nil, nil) if the key does not exist.
func (s *Store) GetKV(tenant, key string) ([]byte, error) {
	var val []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b, _ := tenantBucket(tx, tenant, false)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(key))
		if raw != nil {
			val = make([]byte, len(raw))
			copy(val, raw)
		}
		return nil
	})
	return val, err
}

// DeleteKV removes a key from the tenant's bucket. A missing key is not an error.
func (s *Store) DeleteKV(tenant, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, _ := tenantBucket(tx, tenant, false)
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}

// ScanPrefix iterates all keys in the tenant's bucket that start with prefix,
// calling fn(key, value) for each. Returning an error from fn stops iteration.
func (s *Store) ScanPrefix(tenant, prefix string, fn func(key, val []byte) error) error {
	return s.db.View(func(tx *bolt.Tx) error {
		b, _ := tenantBucket(tx, tenant, false)
		if b == nil {
			return nil
		}
		pfx := []byte(prefix)
		c := b.Cursor()
		for k, v := c.Seek(pfx); k != nil && bytes.HasPrefix(k, pfx); k, v = c.Next() {
			if err := fn(k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

func DBPath(dataDir string) string {
	return filepath.Join(dataDir, "spectral.db")
}

func Backup(dbPath, outPath string) error {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	out, err := createFile(outPath, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	return db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(out)
		return err
	})
}

func Restore(dbPath, inPath string) error {
	if err := Verify(inPath); err != nil {
		return err
	}
	return copyFile(inPath, dbPath, 0o600)
}

func BackupEncrypted(dbPath, outPath, keyB64 string) error {
	key, err := decodeKey(keyB64)
	if err != nil {
		return err
	}
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	out, err := createFile(outPath, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if _, err := out.Write(nonce); err != nil {
		return err
	}
	return db.View(func(tx *bolt.Tx) error {
		var buf bytes.Buffer
		if _, err := tx.WriteTo(&buf); err != nil {
			return err
		}
		ciphertext := gcm.Seal(nil, nonce, buf.Bytes(), nil)
		_, err := out.Write(ciphertext)
		return err
	})
}

func RestoreEncrypted(dbPath, inPath, keyB64 string) error {
	plaintext, err := decryptEncryptedFile(inPath, keyB64)
	if err != nil {
		return err
	}
	if err := verifyPlaintextDB(plaintext, filepath.Dir(inPath)); err != nil {
		return err
	}
	dir := filepath.Dir(dbPath)
	tmp, err := os.CreateTemp(dir, "restore-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(plaintext); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := Verify(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dbPath)
}

func Compact(dbPath, outPath string) error {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	out, err := bolt.Open(outPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	return bolt.Compact(out, db, 0)
}

func createFile(path string, mode uint32) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(mode))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func copyFile(src, dst string, mode uint32) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := createFile(dst, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func Verify(dbPath string) error {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(bucketMeta))
		if meta == nil {
			return errors.New("missing meta bucket")
		}
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return errors.New("missing data bucket")
		}
		version := meta.Get([]byte(keyVersion))
		if version == nil || len(version) == 0 {
			return errors.New("missing db version")
		}
		if int(version[0]) > dbVersion {
			return errors.New("unsupported db version")
		}
		return nil
	})
}

func VerifyEncrypted(path, keyB64 string) error {
	plaintext, err := decryptEncryptedFile(path, keyB64)
	if err != nil {
		return err
	}
	return verifyPlaintextDB(plaintext, filepath.Dir(path))
}

func CompactInPlace(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dir := filepath.Dir(dbPath)
	ts := time.Now().UTC().Format("20060102T150405Z")
	tmpPath := filepath.Join(dir, "spectral.compact."+ts+".db")
	backupPath := dbPath + "." + ts + ".bak"

	if err := Compact(dbPath, tmpPath); err != nil {
		return err
	}
	if err := Verify(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(dbPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dbPath); err != nil {
		_ = os.Rename(backupPath, dbPath)
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func RotateEncryptedBackup(inPath, outPath, oldKeyB64, newKeyB64 string) error {
	plaintext, err := decryptEncryptedFile(inPath, oldKeyB64)
	if err != nil {
		return err
	}
	if err := verifyPlaintextDB(plaintext, filepath.Dir(inPath)); err != nil {
		return err
	}
	return encryptToFile(plaintext, outPath, newKeyB64)
}

func decodeKey(keyB64 string) ([]byte, error) {
	if strings.TrimSpace(keyB64) == "" {
		return nil, errors.New("backup key is required")
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("invalid backup key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("backup key must be 32 bytes (base64-encoded)")
	}
	return key, nil
}

func decryptEncryptedFile(path, keyB64 string) ([]byte, error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, errors.New("invalid encrypted backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := data[:gcm.NonceSize()]
	ciphertext := data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func verifyPlaintextDB(data []byte, dir string) error {
	tmp, err := os.CreateTemp(dir, "verify-*.db")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	return Verify(tmpPath)
}

func encryptToFile(plaintext []byte, outPath, keyB64 string) error {
	key, err := decodeKey(keyB64)
	if err != nil {
		return err
	}
	out, err := createFile(outPath, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if _, err := out.Write(nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	_, err = out.Write(ciphertext)
	return err
}
