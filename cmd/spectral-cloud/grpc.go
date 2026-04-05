package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"google.golang.org/grpc"
)

func startMeshGRPC(ctx context.Context, addr string, authManager *auth.Manager) error {
	if authManager == nil {
		return nil
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	server := grpc.NewServer()
	meshpb.RegisterMeshServiceServer(server, mesh.NewServer(authManager))

	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()

	log.Printf("mesh gRPC listening on %s", addr)
	return server.Serve(lis)
}

func meshAuthManager(cfg authConfig) *auth.Manager {
	if cfg.tenantKeys != nil {
		return cfg.tenantKeys
	}
	key := cfg.apiKey
	if key == "" {
		key = cfg.writeKey
	}
	if key == "" || strings.TrimSpace(cfg.defaultTenant) == "" {
		return nil
	}
	manager, err := auth.NewManagerFromEnv(fmt.Sprintf("%s:%s", cfg.defaultTenant, key))
	if err != nil {
		return nil
	}
	return manager
}
