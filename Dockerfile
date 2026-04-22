FROM --platform=$BUILDPLATFORM golang:1.24.5-alpine AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -o /out/spectral-cloud ./cmd/spectral-cloud

FROM alpine:3.20
RUN adduser -D -H -s /sbin/nologin appuser \
  && mkdir -p /data \
  && chown -R appuser /data
USER appuser
WORKDIR /app
COPY --from=build /out/spectral-cloud /app/spectral-cloud
EXPOSE 8080
ENV PORT=8080
ENV DATA_DIR=/data
ENTRYPOINT ["/app/spectral-cloud"]
