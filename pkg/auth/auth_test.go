package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestNewManagerFromEnv_Valid(t *testing.T) {
	m, err := NewManagerFromEnv("tenant1:key1,tenant2:key2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant, ok := m.TenantFromKey("key1"); !ok || tenant != "tenant1" {
		t.Fatalf("expected tenant1, got %q %v", tenant, ok)
	}
	if tenant, ok := m.TenantFromKey("key2"); !ok || tenant != "tenant2" {
		t.Fatalf("expected tenant2, got %q %v", tenant, ok)
	}
}

func TestNewManagerFromEnv_Empty(t *testing.T) {
	if _, err := NewManagerFromEnv(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestNewManagerFromEnv_InvalidEntry(t *testing.T) {
	if _, err := NewManagerFromEnv("badentry"); err == nil {
		t.Fatal("expected error for entry without colon")
	}
}

func TestNewManagerFromEnv_EmptyTenantOrKey(t *testing.T) {
	if _, err := NewManagerFromEnv(":key"); err == nil {
		t.Fatal("expected error for empty tenant")
	}
	if _, err := NewManagerFromEnv("tenant:"); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestNewManagerFromEnv_Whitespace(t *testing.T) {
	m, err := NewManagerFromEnv("  t1 : k1 , t2 : k2  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := m.TenantFromKey("k1"); !ok {
		t.Fatal("expected k1 to resolve")
	}
}

func TestTenantFromKey_Miss(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:k1")
	if _, ok := m.TenantFromKey("unknown"); ok {
		t.Fatal("expected miss for unknown key")
	}
}

func TestTenantFromRequest_XAPIKey(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:key1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "key1")

	tenant, ok := m.TenantFromRequest(req)
	if !ok || tenant != "t1" {
		t.Fatalf("expected t1, got %q %v", tenant, ok)
	}
}

func TestTenantFromRequest_BearerToken(t *testing.T) {
	m, _ := NewManagerFromEnv("t2:secret")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")

	tenant, ok := m.TenantFromRequest(req)
	if !ok || tenant != "t2" {
		t.Fatalf("expected t2, got %q %v", tenant, ok)
	}
}

func TestTenantFromRequest_BearerCaseInsensitive(t *testing.T) {
	m, _ := NewManagerFromEnv("t3:tok")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "BEARER tok")

	tenant, ok := m.TenantFromRequest(req)
	if !ok || tenant != "t3" {
		t.Fatalf("expected t3, got %q %v", tenant, ok)
	}
}

func TestTenantFromRequest_NoHeader(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:k1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if _, ok := m.TenantFromRequest(req); ok {
		t.Fatal("expected miss when no auth header")
	}
}

func TestTenantFromRequest_WrongKey(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:k1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")

	if _, ok := m.TenantFromRequest(req); ok {
		t.Fatal("expected miss for wrong key")
	}
}

func TestTenantFromContext_XAPIKey(t *testing.T) {
	m, _ := NewManagerFromEnv("ctx-tenant:ctx-key")
	md := metadata.New(map[string]string{"x-api-key": "ctx-key"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	tenant, ok := m.TenantFromContext(ctx)
	if !ok || tenant != "ctx-tenant" {
		t.Fatalf("expected ctx-tenant, got %q %v", tenant, ok)
	}
}

func TestTenantFromContext_Bearer(t *testing.T) {
	m, _ := NewManagerFromEnv("grpc-tenant:grpc-key")
	md := metadata.New(map[string]string{"authorization": "Bearer grpc-key"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	tenant, ok := m.TenantFromContext(ctx)
	if !ok || tenant != "grpc-tenant" {
		t.Fatalf("expected grpc-tenant, got %q %v", tenant, ok)
	}
}

func TestTenantFromContext_NoMetadata(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:k1")
	if _, ok := m.TenantFromContext(context.Background()); ok {
		t.Fatal("expected miss with no metadata in context")
	}
}

func TestTenantFromContext_WrongKey(t *testing.T) {
	m, _ := NewManagerFromEnv("t1:k1")
	md := metadata.New(map[string]string{"x-api-key": "wrong"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	if _, ok := m.TenantFromContext(ctx); ok {
		t.Fatal("expected miss for wrong key in context")
	}
}
