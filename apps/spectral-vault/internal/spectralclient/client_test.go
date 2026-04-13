package spectralclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublish(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotTenant, gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("X-API-Key")
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL: srv.URL,
		APIKey:  "test-api-key",
		Tenant:  "tenant1",
	})
	payload := map[string]any{"file_id": "vf-abc", "size": float64(1024)}
	if err := c.Publish(context.Background(), "ingest", payload); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: expected POST, got %q", gotMethod)
	}
	if gotPath != "/mq/ingest" {
		t.Errorf("path: expected /mq/ingest, got %q", gotPath)
	}
	if gotAuth != "test-api-key" {
		t.Errorf("X-API-Key: expected %q, got %q", "test-api-key", gotAuth)
	}
	if gotTenant != "tenant1" {
		t.Errorf("X-Tenant-ID: expected %q, got %q", "tenant1", gotTenant)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: expected application/json, got %q", gotContentType)
	}
	inner, ok := gotBody["payload"].(map[string]any)
	if !ok {
		t.Fatalf("body.payload missing or wrong type: %v", gotBody)
	}
	if inner["file_id"] != "vf-abc" {
		t.Errorf("payload.file_id: expected %q, got %v", "vf-abc", inner["file_id"])
	}
}

func TestConsume(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	messages := []Message{
		{ID: "msg-1", Topic: "ingest", Tenant: "tenant1", Payload: map[string]any{"x": "y"}, CreatedAt: now},
	}

	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(messages); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Tenant: "tenant1"})
	msgs, err := c.Consume(context.Background(), "ingest", 5)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if gotPath != "/mq/ingest" {
		t.Errorf("path: expected /mq/ingest, got %q", gotPath)
	}
	if gotQuery != "count=5" {
		t.Errorf("query: expected count=5, got %q", gotQuery)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "msg-1" {
		t.Errorf("ID: expected %q, got %q", "msg-1", msgs[0].ID)
	}
}

func TestPublishError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	err := c.Publish(context.Background(), "ingest", map[string]any{})
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}
