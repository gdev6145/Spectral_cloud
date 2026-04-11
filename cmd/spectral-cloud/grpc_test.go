package main

import (
	"context"
	"testing"

	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"google.golang.org/grpc/metadata"
)

func TestMeshAuthManagerPrefersTenantWriteKeys(t *testing.T) {
	tenantWrite, err := auth.NewManagerFromEnv("tenant-grpc:write-key")
	if err != nil {
		t.Fatalf("tenant write manager: %v", err)
	}
	tenantRead, err := auth.NewManagerFromEnv("tenant-grpc:read-key")
	if err != nil {
		t.Fatalf("tenant read manager: %v", err)
	}

	manager := meshAuthManager(authConfig{
		tenantKeys:    tenantRead,
		tenantWrite:   tenantWrite,
		defaultTenant: "default",
	})
	if manager == nil {
		t.Fatal("expected auth manager")
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"x-api-key": "write-key",
	}))
	tenant, ok := manager.TenantFromContext(ctx)
	if !ok || tenant != "tenant-grpc" {
		t.Fatalf("expected tenant-grpc, got %q %v", tenant, ok)
	}
}
