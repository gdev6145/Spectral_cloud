package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/grpc/metadata"
)

type Manager struct {
	keyToTenant map[string]string
}

func NewManagerFromEnv(raw string) (*Manager, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("TENANT_KEYS is empty")
	}
	keyToTenant := map[string]string{}
	pairs := strings.Split(raw, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return nil, errors.New("invalid TENANT_KEYS entry: " + pair)
		}
		tenant := strings.TrimSpace(parts[0])
		key := strings.TrimSpace(parts[1])
		if tenant == "" || key == "" {
			return nil, errors.New("invalid TENANT_KEYS entry: " + pair)
		}
		keyToTenant[key] = tenant
	}
	if len(keyToTenant) == 0 {
		return nil, errors.New("TENANT_KEYS contains no valid entries")
	}
	return &Manager{keyToTenant: keyToTenant}, nil
}

func (m *Manager) TenantFromKey(key string) (string, bool) {
	tenant, ok := m.keyToTenant[key]
	return tenant, ok
}

func (m *Manager) TenantFromRequest(r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" {
		authz := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
			key = strings.TrimSpace(authz[7:])
		}
	}
	if key == "" {
		return "", false
	}
	return m.TenantFromKey(key)
}

func (m *Manager) TenantFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	if keys := md.Get("x-api-key"); len(keys) > 0 {
		if tenant, ok := m.TenantFromKey(strings.TrimSpace(keys[0])); ok {
			return tenant, true
		}
	}
	if authz := md.Get("authorization"); len(authz) > 0 {
		val := strings.TrimSpace(authz[0])
		if strings.HasPrefix(strings.ToLower(val), "bearer ") {
			return m.TenantFromKey(strings.TrimSpace(val[7:]))
		}
	}
	return "", false
}
