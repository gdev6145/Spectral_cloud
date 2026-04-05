# Mesh gRPC TLS Setup

This guide shows how to generate a CA, server cert, and client cert for mesh gRPC TLS/mTLS.

## 1) Generate Certificates

```bash
./scripts/gen-mesh-certs.sh ./certs spectral-cloud spectral-client
```

Artifacts:
- `certs/ca.pem` (CA certificate)
- `certs/server.pem` + `certs/server.key`
- `certs/client.pem` + `certs/client.key`

## 2) Server Configuration

Set TLS env vars before starting the control plane:

```bash
export MESH_GRPC_ADDR=":9091"
export MESH_GRPC_TLS_CERT="./certs/server.pem"
export MESH_GRPC_TLS_KEY="./certs/server.key"
```

To require client certs (mTLS):

```bash
export MESH_GRPC_TLS_CLIENT_CA="./certs/ca.pem"
```

## 3) Client Usage (spectralctl)

```bash
go run ./cmd/spectralctl mesh-send --addr localhost:9091 --api-key devkey --tenant default \
  --kind data --payload "hello" --tls --tls-ca ./certs/ca.pem --tls-server-name spectral-cloud
```

If you are using mTLS with client certs, configure your gRPC client to present `client.pem` and `client.key` in your own tooling.

## 4) Notes

- `--tls-insecure-skip-verify` can be used for testing but is not recommended.
- Server name should match the certificate CN or SAN (`spectral-cloud` in the example).
