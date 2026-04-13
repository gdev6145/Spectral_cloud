package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/db"
	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/organizer"
	"github.com/gdev6145/Spectral_cloud/apps/spectral-vault/internal/spectralclient"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dataDir := getEnv("VAULT_DATA_DIR", "./vault-data")
	spectralURL := os.Getenv("SPECTRAL_URL")
	if spectralURL == "" {
		log.Fatal("SPECTRAL_URL is required")
	}
	spectralAPIKey := getEnv("SPECTRAL_API_KEY", "")
	tenant := getEnv("SPECTRAL_TENANT", "default")
	ingestTopic := getEnv("SPECTRAL_MQ_INGEST_TOPIC", "ingest")
	resultsTopic := getEnv("SPECTRAL_MQ_RESULTS_TOPIC", "ingest.results")

	pollIntervalStr := getEnv("WORKER_POLL_INTERVAL", "5s")
	pollInterval, err := time.ParseDuration(pollIntervalStr)
	if err != nil {
		log.Fatalf("invalid WORKER_POLL_INTERVAL %q: %v", pollIntervalStr, err)
	}

	if err = os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	dbPath := dataDir + "/vault.db"
	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()
	log.Printf("vault-worker: database opened at %s", dbPath)

	mq := spectralclient.New(spectralclient.Config{
		BaseURL: spectralURL,
		APIKey:  spectralAPIKey,
		Tenant:  tenant,
	})

	org := organizer.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("vault-worker: received signal %s, shutting down", sig)
		cancel()
	}()

	log.Printf("vault-worker: polling %s every %s", ingestTopic, pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("vault-worker: stopped")
			return
		case <-ticker.C:
			processMessages(ctx, mq, database, org, tenant, ingestTopic, resultsTopic)
		}
	}
}

func processMessages(
	ctx context.Context,
	mq *spectralclient.Client,
	database *db.DB,
	org *organizer.Organizer,
	tenant, ingestTopic, resultsTopic string,
) {
	msgs, err := mq.Consume(ctx, ingestTopic, 20)
	if err != nil {
		log.Printf("vault-worker: consume error: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}
	log.Printf("vault-worker: processing %d message(s)", len(msgs))

	for _, msg := range msgs {
		if ctx.Err() != nil {
			return
		}
		if err = handleMessage(ctx, msg, mq, database, org, resultsTopic); err != nil {
			log.Printf("vault-worker: message %s error: %v", msg.ID, err)
		}
	}
}

func handleMessage(
	ctx context.Context,
	msg spectralclient.Message,
	mq *spectralclient.Client,
	database *db.DB,
	org *organizer.Organizer,
	resultsTopic string,
) error {
	p := msg.Payload
	fileID, _ := p["file_id"].(string)
	msgTenant, _ := p["tenant"].(string)
	originalName, _ := p["original_name"].(string)
	mime, _ := p["mime"].(string)

	var size int64
	switch v := p["size"].(type) {
	case float64:
		size = int64(v)
	case int64:
		size = v
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			size = n
		}
	}

	if fileID == "" || msgTenant == "" {
		log.Printf("vault-worker: skipping message %s: missing file_id or tenant", msg.ID)
		return nil
	}

	log.Printf("vault-worker: organizing file %s for tenant %s", fileID, msgTenant)

	orgResult := org.Organize(organizer.Input{
		OriginalName: originalName,
		MimeType:     mime,
		Size:         size,
		UploadedAt:   msg.CreatedAt,
	})

	existing, err := database.GetByID(msgTenant, fileID)
	if err != nil {
		return err
	}
	if existing == nil {
		log.Printf("vault-worker: file %s not found in db, skipping update", fileID)
	} else {
		existing.CanonicalName = orgResult.CanonicalName
		existing.FolderPath = orgResult.FolderPath
		existing.Tags = strings.Join(orgResult.Tags, ",")
		existing.Summary = orgResult.Summary
		existing.UpdatedAt = time.Now().UTC()
		if err = database.Update(*existing); err != nil {
			return err
		}
		log.Printf("vault-worker: updated file %s -> %s / %s", fileID, orgResult.FolderPath, orgResult.CanonicalName)
	}

	result := map[string]any{
		"file_id":        fileID,
		"tenant":         msgTenant,
		"canonical_name": orgResult.CanonicalName,
		"folder_path":    orgResult.FolderPath,
		"tags":           orgResult.Tags,
		"status":         "organized",
	}
	if pubErr := mq.Publish(ctx, resultsTopic, result); pubErr != nil {
		log.Printf("vault-worker: publish result for %s: %v", fileID, pubErr)
	}
	return nil
}
