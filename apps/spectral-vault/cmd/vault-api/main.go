package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/db"
	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/organizer"
	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/spectralclient"
	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/storage"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type server struct {
	store        *storage.Store
	database     *db.DB
	org          *organizer.Organizer
	mq           *spectralclient.Client
	tenantHeader string
	mqTopic      string
}

func (s *server) tenantFromRequest(r *http.Request) string {
	if s.tenantHeader != "" {
		if t := r.Header.Get(s.tenantHeader); t != "" {
			return t
		}
	}
	return "default"
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		log.Printf("writeError encode: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode: %v", err)
	}
}

// generateID produces a vf-<8 hex chars> identifier.
func generateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "vf-" + hex.EncodeToString(b), nil
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenant := s.tenantFromRequest(r)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field required")
		return
	}
	defer f.Close()

	folderOverride := strings.TrimSpace(r.FormValue("folder"))

	// Stream content through sha256 hasher and into storage simultaneously.
	id, err := generateID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id generation failed")
		return
	}
	storageKey := tenant + "/" + id + "/" + fh.Filename
	hasher := sha256.New()
	pr, pw := io.Pipe()
	tee := io.TeeReader(f, hasher)

	type putResult struct {
		n   int64
		err error
	}
	putCh := make(chan putResult, 1)
	go func() {
		n, err := s.store.Put(tenant, storageKey, pr)
		putCh <- putResult{n, err}
		pr.CloseWithError(err)
	}()

	if _, err = io.Copy(pw, tee); err != nil {
		pw.CloseWithError(err)
		writeError(w, http.StatusInternalServerError, "read upload failed")
		return
	}
	pw.Close()

	res := <-putCh
	if res.err != nil {
		writeError(w, http.StatusInternalServerError, "store failed")
		return
	}

	sha := hex.EncodeToString(hasher.Sum(nil))

	// Deduplicate by SHA256.
	existing, err := s.database.GetBySHA256(tenant, sha)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if existing != nil {
		// Remove the just-stored duplicate file.
		if delErr := s.store.Delete(tenant, storageKey); delErr != nil {
			log.Printf("cleanup duplicate storage: delete failed")
		}
		writeJSON(w, http.StatusConflict, existing)
		return
	}

	mime := fh.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	now := time.Now().UTC()
	orgResult := s.org.Organize(organizer.Input{
		OriginalName: fh.Filename,
		MimeType:     mime,
		Size:         res.n,
		UploadedAt:   now,
	})

	folder := orgResult.FolderPath
	if folderOverride != "" {
		folder = folderOverride
	}

	rec := db.FileRecord{
		ID:            id,
		Tenant:        tenant,
		OriginalName:  fh.Filename,
		CanonicalName: orgResult.CanonicalName,
		FolderPath:    folder,
		StorageKey:    storageKey,
		MimeType:      mime,
		Size:          res.n,
		SHA256:        sha,
		Tags:          strings.Join(orgResult.Tags, ","),
		Summary:       orgResult.Summary,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err = s.database.Insert(rec); err != nil {
		writeError(w, http.StatusInternalServerError, "db insert failed")
		return
	}

	// Publish to MQ asynchronously.
	if s.mq != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if pubErr := s.mq.Publish(ctx, s.mqTopic, map[string]any{
				"file_id":       id,
				"tenant":        tenant,
				"storage_key":   storageKey,
				"original_name": fh.Filename,
				"sha256":        sha,
				"mime":          mime,
				"size":          res.n,
			}); pubErr != nil {
				log.Printf("mq publish: %v", pubErr)
			}
		}()
	}

	writeJSON(w, http.StatusCreated, rec)
}

func (s *server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenant := s.tenantFromRequest(r)
	id := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	id = strings.TrimSuffix(id, "/download")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	rec, err := s.database.GetByID(tenant, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenant := s.tenantFromRequest(r)
	// Path: /v1/files/{id}/download
	trimmed := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	id := strings.TrimSuffix(trimmed, "/download")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	rec, err := s.database.GetByID(tenant, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	rc, err := s.store.Get(tenant, rec.StorageKey)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found in storage")
		return
	}
	defer rc.Close()

	if rec.MimeType != "" {
		w.Header().Set("Content-Type", rec.MimeType)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, rec.OriginalName))
	w.WriteHeader(http.StatusOK)
	if _, err = io.Copy(w, rc); err != nil {
		log.Printf("download copy: %v", err)
	}
}

func (s *server) handleListFolders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenant := s.tenantFromRequest(r)
	prefix := r.URL.Query().Get("prefix")
	folders, err := s.database.ListFolders(tenant, prefix)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if folders == nil {
		folders = []string{}
	}
	writeJSON(w, http.StatusOK, folders)
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenant := s.tenantFromRequest(r)
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter required")
		return
	}
	results, err := s.database.Search(tenant, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if results == nil {
		results = []db.FileRecord{}
	}
	writeJSON(w, http.StatusOK, results)
}

func main() {
	dataDir := getEnv("VAULT_DATA_DIR", "./vault-data")
	addr := getEnv("VAULT_ADDR", ":8090")
	tenantHeader := getEnv("VAULT_TENANT_HEADER", "")
	spectralURL := getEnv("SPECTRAL_URL", "")
	spectralAPIKey := getEnv("SPECTRAL_API_KEY", "")
	mqTopic := getEnv("SPECTRAL_MQ_INGEST_TOPIC", "ingest")

	st, err := storage.New(dataDir)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	dbPath := dataDir + "/vault.db"
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	org := organizer.New()

	var mqClient *spectralclient.Client
	if spectralURL != "" {
		mqClient = spectralclient.New(spectralclient.Config{
			BaseURL: spectralURL,
			APIKey:  spectralAPIKey,
		})
	}

	srv := &server{
		store:        st,
		database:     database,
		org:          org,
		mq:           mqClient,
		tenantHeader: tenantHeader,
		mqTopic:      mqTopic,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/v1/files", srv.handleUpload)
	mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			srv.handleDownload(w, r)
		} else {
			srv.handleGetFile(w, r)
		}
	})
	mux.HandleFunc("/v1/folders", srv.handleListFolders)
	mux.HandleFunc("/v1/search", srv.handleSearch)

	log.Printf("vault-api listening on %s", addr)
	if err = http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
