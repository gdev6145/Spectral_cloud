#!/usr/bin/env bash
set -euo pipefail

export PATH="$(go env GOPATH)/bin:${PATH}"

if ! command -v protoc >/dev/null 2>&1; then
  echo "protoc not found. Install protobuf compiler to generate Go files."
  exit 1
fi

if ! command -v protoc-gen-go >/dev/null 2>&1; then
  echo "protoc-gen-go not found. Install with: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"
  exit 1
fi

if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
  echo "protoc-gen-go-grpc not found. Install with: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"
  exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

protoc \
  --proto_path="${ROOT}/proto" \
  --go_out="${ROOT}/pkg/proto" \
  --go_opt=paths=source_relative \
  --go-grpc_out="${ROOT}/pkg/proto" \
  --go-grpc_opt=paths=source_relative \
  "${ROOT}/proto/mesh.proto"
