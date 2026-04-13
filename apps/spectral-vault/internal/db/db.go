package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// FileRecord holds metadata for an uploaded file.
type FileRecord struct {
	ID            string    `json:"id"`
	Tenant        string    `json:"tenant"`
	OriginalName  string    `json:"original_name"`
	CanonicalName string    `json:"canonical_name"`
	FolderPath    string    `json:"folder_path"`
	StorageKey    string    `json:"storage_key"`
	MimeType      string    `json:"mime_type"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	Tags          string    `json:"tags"` // comma-separated
	Summary       string    `json:"summary"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// DB wraps a SQLite connection with catalog operations.
type DB struct {
	conn *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS files (
  id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  original_name TEXT NOT NULL,
  canonical_name TEXT NOT NULL,
  folder_path TEXT NOT NULL,
  storage_key TEXT NOT NULL,
  mime_type TEXT,
  size INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT,
  tags TEXT,
  summary TEXT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_tenant ON files(tenant);
CREATE INDEX IF NOT EXISTS idx_files_sha256 ON files(tenant, sha256);
`

// Open opens or creates the SQLite database at path and runs the schema migration.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	if _, err = conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: migrate: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}

// Insert adds a new FileRecord to the database.
func (d *DB) Insert(rec FileRecord) error {
	_, err := d.conn.Exec(`
		INSERT INTO files
		  (id, tenant, original_name, canonical_name, folder_path, storage_key,
		   mime_type, size, sha256, tags, summary, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.Tenant, rec.OriginalName, rec.CanonicalName, rec.FolderPath,
		rec.StorageKey, rec.MimeType, rec.Size, rec.SHA256, rec.Tags, rec.Summary,
		rec.CreatedAt.UTC(), rec.UpdatedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("db: insert: %w", err)
	}
	return nil
}

// Update updates the mutable fields of a FileRecord (canonical_name, folder_path, tags, summary).
func (d *DB) Update(rec FileRecord) error {
	_, err := d.conn.Exec(`
		UPDATE files SET canonical_name=?, folder_path=?, tags=?, summary=?, updated_at=?
		WHERE id=? AND tenant=?`,
		rec.CanonicalName, rec.FolderPath, rec.Tags, rec.Summary, rec.UpdatedAt.UTC(),
		rec.ID, rec.Tenant,
	)
	if err != nil {
		return fmt.Errorf("db: update: %w", err)
	}
	return nil
}

// GetByID fetches a single record by tenant + id.
func (d *DB) GetByID(tenant, id string) (*FileRecord, error) {
	row := d.conn.QueryRow(`
		SELECT id, tenant, original_name, canonical_name, folder_path, storage_key,
		       mime_type, size, sha256, tags, summary, created_at, updated_at
		FROM files WHERE tenant=? AND id=?`, tenant, id)
	return scanRecord(row)
}

// GetBySHA256 returns the first record matching tenant + sha256, used for dedupe checks.
func (d *DB) GetBySHA256(tenant, sha256 string) (*FileRecord, error) {
	row := d.conn.QueryRow(`
		SELECT id, tenant, original_name, canonical_name, folder_path, storage_key,
		       mime_type, size, sha256, tags, summary, created_at, updated_at
		FROM files WHERE tenant=? AND sha256=? LIMIT 1`, tenant, sha256)
	return scanRecord(row)
}

// Search does a fuzzy (LIKE %q%) match across original_name, canonical_name, tags, and summary.
func (d *DB) Search(tenant, q string) ([]FileRecord, error) {
	// Escape LIKE special characters so user input is treated as a literal string.
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	like := "%" + escaped + "%"
	rows, err := d.conn.Query(`
		SELECT id, tenant, original_name, canonical_name, folder_path, storage_key,
		       mime_type, size, sha256, tags, summary, created_at, updated_at
		FROM files
		WHERE tenant=? AND (
		  original_name  LIKE ? ESCAPE '\' OR
		  canonical_name LIKE ? ESCAPE '\' OR
		  tags           LIKE ? ESCAPE '\' OR
		  summary        LIKE ? ESCAPE '\'
		)
		ORDER BY created_at DESC`, tenant, like, like, like, like)
	if err != nil {
		return nil, fmt.Errorf("db: search: %w", err)
	}
	defer rows.Close()
	return collectRows(rows)
}

// ListFolders returns distinct folder_paths that start with prefix for the given tenant.
func (d *DB) ListFolders(tenant, prefix string) ([]string, error) {
	rows, err := d.conn.Query(`
		SELECT DISTINCT folder_path FROM files
		WHERE tenant=? AND folder_path LIKE ?
		ORDER BY folder_path`, tenant, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("db: list folders: %w", err)
	}
	defer rows.Close()
	var folders []string
	for rows.Next() {
		var fp string
		if err = rows.Scan(&fp); err != nil {
			return nil, fmt.Errorf("db: list folders scan: %w", err)
		}
		folders = append(folders, fp)
	}
	return folders, rows.Err()
}

// scanRecord scans a single Row into a FileRecord.
func scanRecord(row *sql.Row) (*FileRecord, error) {
	var rec FileRecord
	var createdAt, updatedAt string
	err := row.Scan(
		&rec.ID, &rec.Tenant, &rec.OriginalName, &rec.CanonicalName,
		&rec.FolderPath, &rec.StorageKey, &rec.MimeType, &rec.Size,
		&rec.SHA256, &rec.Tags, &rec.Summary, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("db: scan: %w", err)
	}
	rec.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, err
	}
	rec.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// collectRows scans multiple rows into a slice of FileRecord.
func collectRows(rows *sql.Rows) ([]FileRecord, error) {
	var recs []FileRecord
	for rows.Next() {
		var rec FileRecord
		var createdAt, updatedAt string
		err := rows.Scan(
			&rec.ID, &rec.Tenant, &rec.OriginalName, &rec.CanonicalName,
			&rec.FolderPath, &rec.StorageKey, &rec.MimeType, &rec.Size,
			&rec.SHA256, &rec.Tags, &rec.Summary, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("db: scan row: %w", err)
		}
		rec.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		rec.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
	}
	return recs, rows.Err()
}

// parseTime handles SQLite datetime strings in multiple layouts.
func parseTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("db: cannot parse time %q", s)
}
