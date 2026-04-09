package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
)

type tenantState struct {
	chain  *blockchain.Blockchain
	router *routing.RoutingEngine
}

type tenantManager struct {
	store   *store.Store
	mu      sync.RWMutex
	tenants map[string]*tenantState
}

func newTenantManager(store *store.Store) *tenantManager {
	return &tenantManager{
		store:   store,
		tenants: map[string]*tenantState{},
	}
}

func (tm *tenantManager) getTenant(tenant string) (*tenantState, error) {
	tm.mu.RLock()
	if state, ok := tm.tenants[tenant]; ok {
		tm.mu.RUnlock()
		return state, nil
	}
	tm.mu.RUnlock()

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if state, ok := tm.tenants[tenant]; ok {
		return state, nil
	}

	if err := tm.store.EnsureTenant(tenant); err != nil {
		return nil, err
	}

	blocks, err := tm.store.ReadBlocksTenant(tenant)
	if err != nil {
		return nil, err
	}
	chain := blockchain.NewBlockchain()
	if len(blocks) > 0 {
		valid := filterValidBlocks(blocks)
		chain.Load(valid)
		if len(valid) != len(blocks) {
			if err := tm.store.WriteBlocksTenant(tenant, valid); err != nil {
				return nil, err
			}
		}
	} else {
		if err := tm.store.SaveChainTenant(tenant, chain); err != nil {
			return nil, fmt.Errorf("persist genesis: %w", err)
		}
	}

	routes, err := tm.store.ReadRoutesTenant(tenant)
	if err != nil {
		return nil, err
	}
	router := routing.NewRoutingEngine()
	if len(routes) > 0 {
		pruned := pruneExpiredRoutes(routes)
		router.Load(pruned)
		if len(pruned) != len(routes) {
			if err := tm.store.WriteRoutesTenant(tenant, pruned); err != nil {
				return nil, err
			}
		}
	}

	state := &tenantState{
		chain:  chain,
		router: router,
	}
	tm.tenants[tenant] = state
	return state, nil
}

// evict removes a tenant from the in-memory cache. It is called after the
// tenant's store bucket is deleted so stale state is not re-served.
func (tm *tenantManager) evict(tenant string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.tenants, tenant)
}

type tenantContextKey struct{}

func withTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenant)
}

func tenantFromContext(ctx context.Context) (string, bool) {
	val := ctx.Value(tenantContextKey{})
	if val == nil {
		return "", false
	}
	tenant, ok := val.(string)
	return tenant, ok
}
