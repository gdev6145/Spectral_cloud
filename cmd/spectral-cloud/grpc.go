package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func startMeshGRPC(ctx context.Context, addr string, authManager *auth.Manager, tlsCfg *tls.Config) error {
	if authManager == nil {
		return nil
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	var opts []grpc.ServerOption
	if tlsCfg != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	server := grpc.NewServer(opts...)
	meshpb.RegisterMeshServiceServer(server, mesh.NewServer(authManager))

	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()

	log.Printf("mesh gRPC listening on %s", addr)
	return server.Serve(lis)
}

func meshAuthManager(cfg authConfig) *auth.Manager {
	if cfg.tenantWrite != nil {
		return cfg.tenantWrite
	}
	if cfg.tenantKeys != nil {
		return cfg.tenantKeys
	}
	key := cfg.writeKey
	if key == "" {
		key = cfg.apiKey
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

func loadMeshTLSConfig() (*tls.Config, error) {
	certPath := strings.TrimSpace(os.Getenv("MESH_GRPC_TLS_CERT"))
	keyPath := strings.TrimSpace(os.Getenv("MESH_GRPC_TLS_KEY"))
	if certPath == "" || keyPath == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	caPath := strings.TrimSpace(os.Getenv("MESH_GRPC_TLS_CLIENT_CA"))
	if caPath != "" {
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("failed to parse client CA")
		}
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		tlsCfg.ClientCAs = pool
	}
	return tlsCfg, nil
}
