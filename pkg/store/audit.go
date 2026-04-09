package store

import (
	"encoding/binary"
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

const bucketAudit = "audit"

// AuditEntry records a single mutating API call.
type AuditEntry struct {
	ID        uint64    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Tenant    string    `json:"tenant"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	Detail    string    `json:"detail,omitempty"`
}

// AuditRecord appends an entry to the audit log. Thread-safe.
func (s *Store) AuditRecord(e AuditEntry) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucketAudit))
		if err != nil {
			return err
		}
		id, _ := b.NextSequence()
		e.ID = id
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, id)
		return b.Put(key, data)
	})
}

// AuditList returns the most recent entries, newest first.
// Pass tenant="" to return all tenants. limit<=0 defaults to 100.
func (s *Store) AuditList(tenant string, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	var entries []AuditEntry
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAudit))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Last(); k != nil && len(entries) < limit; k, v = c.Prev() {
			var e AuditEntry
			if err := json.Unmarshal(v, &e); err != nil {
				continue
			}
			if tenant != "" && e.Tenant != tenant {
				continue
			}
			entries = append(entries, e)
		}
		return nil
	})
	return entries, err
}
