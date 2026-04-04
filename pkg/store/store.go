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

	bucketMeta = "meta"
	bucketData = "data"

	keyVersion = "version"
	keyBlocks  = "blocks"
	keyRoutes  = "routes"
)

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

func (s *Store) init() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketData)); err != nil {
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
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		if data.Get([]byte(keyBlocks)) != nil || data.Get([]byte(keyRoutes)) != nil {
			has = true
		}
		return nil
	})
	return has, err
}

func (s *Store) Load(chain *blockchain.Blockchain, router *routing.RoutingEngine) error {
	return s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		if raw := data.Get([]byte(keyBlocks)); raw != nil {
			var blocks []blockchain.Block
			if err := json.Unmarshal(raw, &blocks); err != nil {
				return err
			}
			chain.Load(blocks)
		}
		if raw := data.Get([]byte(keyRoutes)); raw != nil {
			var routes []routing.Route
			if err := json.Unmarshal(raw, &routes); err != nil {
				return err
			}
			router.Load(routes)
		}
		return nil
	})
}

func (s *Store) ReadBlocks() ([]blockchain.Block, error) {
	var blocks []blockchain.Block
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		raw := data.Get([]byte(keyBlocks))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &blocks)
	})
	return blocks, err
}

func (s *Store) ReadRoutes() ([]routing.Route, error) {
	var routes []routing.Route
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte(bucketData))
		if data == nil {
			return nil
		}
		raw := data.Get([]byte(keyRoutes))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &routes)
	})
	return routes, err
}

func (s *Store) WriteBlocks(blocks []blockchain.Block) error {
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		return b.Put([]byte(keyBlocks), data)
	})
}

func (s *Store) WriteRoutes(routes []routing.Route) error {
	data, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		return b.Put([]byte(keyRoutes), data)
	})
}

func (s *Store) SaveChain(chain *blockchain.Blockchain) error {
	blocks := chain.Snapshot()
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		return b.Put([]byte(keyBlocks), data)
	})
}

func (s *Store) SaveRoutes(router *routing.RoutingEngine) error {
	routes := router.ListRoutes()
	data, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		return b.Put([]byte(keyRoutes), data)
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
