package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Store is a local filesystem object store that organises files under DataDir.
type Store struct {
	DataDir string
}

// New creates a new Store rooted at dataDir, creating the directory if needed.
func New(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create data dir: %w", err)
	}
	return &Store{DataDir: dataDir}, nil
}

// StoragePath returns the on-disk path for the given tenant + storageKey.
func (s *Store) StoragePath(tenant, storageKey string) string {
	return filepath.Join(s.DataDir, tenant, storageKey)
}

// Put writes the contents of r to the store and returns the number of bytes written.
func (s *Store) Put(tenant, storageKey string, r io.Reader) (int64, error) {
	dest := s.StoragePath(tenant, storageKey)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, fmt.Errorf("storage: create parent dirs: %w", err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, fmt.Errorf("storage: create file: %w", err)
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return n, fmt.Errorf("storage: write file: %w", err)
	}
	return n, nil
}

// Get opens the stored file for reading. The caller is responsible for closing the returned ReadCloser.
func (s *Store) Get(tenant, storageKey string) (io.ReadCloser, error) {
	path := s.StoragePath(tenant, storageKey)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("storage: file not found: %s", path)
		}
		return nil, fmt.Errorf("storage: open file: %w", err)
	}
	return f, nil
}

// Delete removes the stored file.
func (s *Store) Delete(tenant, storageKey string) error {
	path := s.StoragePath(tenant, storageKey)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("storage: file not found: %s", path)
		}
		return fmt.Errorf("storage: delete file: %w", err)
	}
	return nil
}
