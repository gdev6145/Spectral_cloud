# syntax=docker/dockerfile:1
# ──────────────────────────────────────────────────────────────────────────────
# Stage 1: build both vault-api and vault-worker
# ──────────────────────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/vault-api  ./cmd/vault-api  && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/vault-worker ./cmd/vault-worker

# ──────────────────────────────────────────────────────────────────────────────
# Stage 2: minimal runtime image
# ──────────────────────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates && \
    addgroup -S vault && adduser -S vault -G vault

COPY --from=builder /out/vault-api    /usr/local/bin/vault-api
COPY --from=builder /out/vault-worker /usr/local/bin/vault-worker

USER vault
VOLUME ["/data"]

# vault-api listens on 8090 by default
EXPOSE 8090

ENV VAULT_DATA_DIR=/data \
    VAULT_ADDR=:8090 \
    VAULT_TENANT_HEADER="" \
    SPECTRAL_URL="" \
    SPECTRAL_API_KEY="" \
    SPECTRAL_MQ_INGEST_TOPIC=ingest \
    SPECTRAL_MQ_RESULTS_TOPIC=ingest.results

CMD ["/usr/local/bin/vault-api"]
