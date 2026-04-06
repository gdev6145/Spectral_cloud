package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/gdev6145/Spectral_cloud/pkg/webhook"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"
	"os/signal"
)

type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Blocks    int    `json:"blocks"`
	Routes    int    `json:"routes"`
}

type statusSnapshot struct {
	Now                 string  `json:"now"`
	UptimeSeconds       int64   `json:"uptime_seconds"`
	LastBackup          *string `json:"last_backup,omitempty"`
	LastBackupError     *string `json:"last_backup_error,omitempty"`
	LastCompact         *string `json:"last_compact,omitempty"`
	LastCompactError    *string `json:"last_compact_error,omitempty"`
	BackupInterval      string  `json:"backup_interval"`
	CompactInterval     string  `json:"compact_interval"`
	BackupDir           string  `json:"backup_dir"`
	CompactionDir       string  `json:"compaction_dir"`
	BackupRetention     int     `json:"backup_retention"`
	CompactionRetention int     `json:"compaction_retention"`
}

type statusTracker struct {
	startedAt           time.Time
	backupInterval      string
	compactInterval     string
	backupDir           string
	compactionDir       string
	backupRetention     int
	compactionRetention int

	mu               sync.Mutex
	lastBackup       *time.Time
	lastBackupError  string
	lastCompact      *time.Time
	lastCompactError string
}

type meshAnomalyState struct {
	mu             sync.Mutex
	lastAt         *time.Time
	lastReason     string
	lastRejectRate float64
	lastReceived   uint64
}

type authConfig struct {
	apiKey        string
	adminKey      string
	adminWriteKey string
	writeKey      string
	tenantKeys    *auth.Manager
	tenantWrite   *auth.Manager
	defaultTenant string
	publicRules   []pathRule
	adminRules    []pathRule
	allowRemote   bool
	adminCIDRs    []*net.IPNet
}

type tenantLimits struct {
	maxBlocks int
	maxRoutes int
}

type pathRule struct {
	value  string
	prefix bool
	method string
}

func (s *statusTracker) snapshot() statusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	var lastBackup, lastCompact *string
	if s.lastBackup != nil {
		val := s.lastBackup.UTC().Format(time.RFC3339)
		lastBackup = &val
	}
	if s.lastCompact != nil {
		val := s.lastCompact.UTC().Format(time.RFC3339)
		lastCompact = &val
	}
	var backupErr, compactErr *string
	if strings.TrimSpace(s.lastBackupError) != "" {
		backupErr = &s.lastBackupError
	}
	if strings.TrimSpace(s.lastCompactError) != "" {
		compactErr = &s.lastCompactError
	}
	return statusSnapshot{
		Now:                 now.Format(time.RFC3339),
		UptimeSeconds:       int64(now.Sub(s.startedAt).Seconds()),
		LastBackup:          lastBackup,
		LastBackupError:     backupErr,
		LastCompact:         lastCompact,
		LastCompactError:    compactErr,
		BackupInterval:      s.backupInterval,
		CompactInterval:     s.compactInterval,
		BackupDir:           s.backupDir,
		CompactionDir:       s.compactionDir,
		BackupRetention:     s.backupRetention,
		CompactionRetention: s.compactionRetention,
	}
}

func (s *statusTracker) recordBackup(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.lastBackup = &now
	if err != nil {
		s.lastBackupError = err.Error()
	} else {
		s.lastBackupError = ""
	}
}

func (s *statusTracker) recordCompact(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.lastCompact = &now
	if err != nil {
		s.lastCompactError = err.Error()
	} else {
		s.lastCompactError = ""
	}
}

type blockchainFile struct {
	Version   int                `json:"version"`
	UpdatedAt string             `json:"updated_at"`
	Blocks    []blockchain.Block `json:"blocks"`
	Meta      map[string]string  `json:"meta,omitempty"`
}

type routesFile struct {
	Version   int               `json:"version"`
	UpdatedAt string            `json:"updated_at"`
	Routes    []routing.Route   `json:"routes"`
	Meta      map[string]string `json:"meta,omitempty"`
}

func main() {
	startedAt := time.Now().UTC()
	maxBodyBytes := getEnvInt("MAX_BODY_BYTES", 1<<20)
	backupRetention := getEnvInt("BACKUP_RETENTION", 5)
	defaultTenant := strings.TrimSpace(os.Getenv("DEFAULT_TENANT"))
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	dataDir := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if dataDir == "" {
		dataDir = "data"
	}
	dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
	if dbPath == "" {
		dbPath = store.DBPath(dataDir)
	}
	if getEnvBool("COMPACT_ON_START", false) {
		if err := store.CompactInPlace(dbPath); err != nil {
			log.Printf("compact on start failed: %v", err)
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("failed to create data dir: %v", err)
	}
	if backedUp, err := backupFile(dbPath); err != nil {
		log.Printf("failed to backup db: %v", err)
	} else if backedUp {
		_ = rotateBackups(dbPath, backupRetention)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	hasData, err := db.HasData()
	if err != nil {
		log.Fatalf("failed to check store data: %v", err)
	}
	if !hasData {
		legacyLoaded, err := migrateLegacyJSON(dataDir, db, defaultTenant, backupRetention)
		if err != nil {
			log.Printf("legacy migration failed: %v", err)
		} else if legacyLoaded {
			log.Printf("legacy data migrated into BoltDB")
		}
	}

	tenantMgr := newTenantManager(db)
	if _, err := tenantMgr.getTenant(defaultTenant); err != nil {
		log.Fatalf("failed to load default tenant: %v", err)
	}

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "spectral_cloud_requests_total",
			Help: "Total HTTP requests processed.",
		},
		[]string{"path", "method", "code"},
	)
	prometheus.MustRegister(requestsTotal)

	meshPackets := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "spectral_cloud_mesh_packets_total",
			Help: "Total mesh packets by outcome.",
		},
		[]string{"outcome"},
	)
	prometheus.MustRegister(meshPackets)

	meshRejectRate := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "spectral_cloud_mesh_reject_rate",
			Help: "Recent mesh packet reject rate.",
		},
	)
	prometheus.MustRegister(meshRejectRate)

	meshAnomaly := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "spectral_cloud_mesh_anomaly",
			Help: "Mesh anomaly flags.",
		},
		[]string{"type"},
	)
	prometheus.MustRegister(meshAnomaly)

	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "spectral_cloud_request_duration_seconds",
			Help:    "HTTP request latency distribution.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
	prometheus.MustRegister(requestDuration)

	authKey := strings.TrimSpace(os.Getenv("API_KEY"))
	adminKey := strings.TrimSpace(os.Getenv("ADMIN_API_KEY"))
	adminWriteKey := strings.TrimSpace(os.Getenv("ADMIN_WRITE_KEY"))
	writeKey := strings.TrimSpace(os.Getenv("WRITE_API_KEY"))
	tenantKeysRaw := strings.TrimSpace(os.Getenv("TENANT_KEYS"))
	tenantWriteRaw := strings.TrimSpace(os.Getenv("TENANT_WRITE_KEYS"))
	var tenantKeys *auth.Manager
	var tenantWrite *auth.Manager
	if tenantKeysRaw != "" {
		manager, err := auth.NewManagerFromEnv(tenantKeysRaw)
		if err != nil {
			log.Fatalf("invalid TENANT_KEYS: %v", err)
		}
		tenantKeys = manager
	}
	if tenantWriteRaw != "" {
		manager, err := auth.NewManagerFromEnv(tenantWriteRaw)
		if err != nil {
			log.Fatalf("invalid TENANT_WRITE_KEYS: %v", err)
		}
		tenantWrite = manager
	}
	if tenantKeys != nil && authKey != "" {
		log.Printf("API_KEY ignored because TENANT_KEYS is set")
	}
	rateRPS := getEnvFloat("RATE_LIMIT_RPS", 10)
	rateBurst := getEnvInt("RATE_LIMIT_BURST", 20)
	tenantRateRPS := getEnvFloat("TENANT_RATE_RPS", 0)
	tenantRateBurst := getEnvInt("TENANT_RATE_BURST", 0)
	tenantMaxBlocks := getEnvInt("TENANT_MAX_BLOCKS", 0)
	tenantMaxRoutes := getEnvInt("TENANT_MAX_ROUTES", 0)

	backupInterval := strings.TrimSpace(os.Getenv("BACKUP_INTERVAL"))
	backupDir := strings.TrimSpace(os.Getenv("BACKUP_DIR"))
	if backupDir == "" {
		backupDir = filepath.Join(dataDir, "backups")
	}
	compactInterval := strings.TrimSpace(os.Getenv("COMPACT_INTERVAL"))
	compactDir := strings.TrimSpace(os.Getenv("COMPACT_DIR"))
	if compactDir == "" {
		compactDir = filepath.Join(dataDir, "compactions")
	}
	compactRetention := getEnvInt("COMPACT_RETENTION", 3)

	status := &statusTracker{
		startedAt:           startedAt,
		backupInterval:      backupInterval,
		compactInterval:     compactInterval,
		backupDir:           backupDir,
		compactionDir:       compactDir,
		backupRetention:     backupRetention,
		compactionRetention: compactRetention,
	}

	adminCIDRs, err := parseCIDRList(strings.TrimSpace(os.Getenv("ADMIN_ALLOWLIST_CIDRS")))
	if err != nil {
		log.Printf("invalid ADMIN_ALLOWLIST_CIDRS: %v", err)
	}

	publicRules := parsePathRules(os.Getenv("PUBLIC_PATHS"), []string{"/health", "/ready"})
	adminRules := parsePathRules(os.Getenv("ADMIN_PATHS"), []string{"/admin/status"})

	auth := authConfig{
		apiKey:        authKey,
		adminKey:      adminKey,
		adminWriteKey: adminWriteKey,
		writeKey:      writeKey,
		tenantKeys:    tenantKeys,
		tenantWrite:   tenantWrite,
		defaultTenant: defaultTenant,
		publicRules:   publicRules,
		adminRules:    adminRules,
		allowRemote:   getEnvBool("ADMIN_ALLOW_REMOTE", false),
		adminCIDRs:    adminCIDRs,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var meshNode *mesh.Node
	meshTenant := strings.TrimSpace(os.Getenv("MESH_TENANT"))
	if meshTenant == "" {
		meshTenant = defaultTenant
	}
	if getEnvBool("MESH_ENABLE", false) {
		meshCfg, err := loadMeshConfig()
		if err != nil {
			log.Printf("mesh disabled: %v", err)
		} else {
			meshCfg.TenantID = meshTenant
			meshState, err := tenantMgr.getTenant(meshTenant)
			if err != nil {
				log.Printf("mesh start failed: %v", err)
			} else if node, err := mesh.Start(ctx, meshCfg, meshState.router); err != nil {
				log.Printf("mesh start failed: %v", err)
			} else {
				meshNode = node
				log.Printf("mesh enabled on %s with %d peers (tenant=%s)", meshCfg.BindAddr, len(meshCfg.Peers), meshTenant)
			}
		}
	}

	meshGrpcAddr := strings.TrimSpace(os.Getenv("MESH_GRPC_ADDR"))
	if meshGrpcAddr != "" {
		authManager := meshAuthManager(auth)
		if authManager == nil {
			log.Printf("mesh gRPC disabled: no auth keys configured")
		} else {
			tlsCfg, err := loadMeshTLSConfig()
			if err != nil {
				log.Printf("mesh gRPC TLS config error: %v", err)
			}
			go func() {
				if err := startMeshGRPC(ctx, meshGrpcAddr, authManager, tlsCfg); err != nil {
					log.Printf("mesh gRPC failed: %v", err)
				}
			}()
		}
	}

	anomalyState := &meshAnomalyState{}
	limits := tenantLimits{
		maxBlocks: tenantMaxBlocks,
		maxRoutes: tenantMaxRoutes,
	}

	agentReg := agent.NewRegistry()
	go func() {
		// Prune expired agents every minute.
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				agentReg.Prune()
			}
		}
	}()

	eventBroker := events.NewBroker()

	webhookURL := strings.TrimSpace(os.Getenv("WEBHOOK_URL"))
	webhookSecret := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET"))
	webhookTimeout := strings.TrimSpace(os.Getenv("WEBHOOK_TIMEOUT"))
	var webhookDur time.Duration
	if webhookTimeout != "" {
		if d, err := time.ParseDuration(webhookTimeout); err == nil {
			webhookDur = d
		}
	}
	hookDispatcher := webhook.New(webhookURL, webhookSecret, webhookDur)

	blockSigningKey := strings.TrimSpace(os.Getenv("BLOCK_SIGNING_KEY"))

	corsCfg := parseCORSConfig(
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")),
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_METHODS")),
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_HEADERS")),
		strings.TrimSpace(os.Getenv("CORS_MAX_AGE")),
	)
	enableAccessLog := getEnvBool("ACCESS_LOG", false)

	handler := newHandler(tenantMgr, db, maxBodyBytes, requestsTotal, meshPackets, meshRejectRate, meshAnomaly, requestDuration, auth, rateRPS, rateBurst, tenantRateRPS, tenantRateBurst, limits, status, meshNode, anomalyState, agentReg, corsCfg, enableAccessLog, eventBroker, hookDispatcher, blockSigningKey)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if backupInterval != "" {
		if d, err := time.ParseDuration(backupInterval); err != nil {
			log.Printf("invalid BACKUP_INTERVAL: %v", err)
		} else if d > 0 {
			if err := os.MkdirAll(backupDir, 0o755); err != nil {
				log.Printf("failed to create backup dir: %v", err)
			} else {
				go runBackupScheduler(ctx, dbPath, backupDir, d, backupRetention, status)
			}
		}
	}
	if compactInterval != "" {
		if d, err := time.ParseDuration(compactInterval); err != nil {
			log.Printf("invalid COMPACT_INTERVAL: %v", err)
		} else if d > 0 {
			if err := os.MkdirAll(compactDir, 0o755); err != nil {
				log.Printf("failed to create compaction dir: %v", err)
			} else {
				go runCompactScheduler(ctx, dbPath, compactDir, d, compactRetention, status)
			}
		}
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("spectral-cloud listening on :%s", port)
		certFile := strings.TrimSpace(os.Getenv("TLS_CERT_FILE"))
		keyFile := strings.TrimSpace(os.Getenv("TLS_KEY_FILE"))
		if certFile != "" && keyFile != "" {
			errCh <- srv.ListenAndServeTLS(certFile, keyFile)
			return
		}
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}
}

func newHandler(tenantMgr *tenantManager, db *store.Store, maxBodyBytes int, requestsTotal *prometheus.CounterVec, meshPackets *prometheus.CounterVec, meshRejectRate prometheus.Gauge, meshAnomaly *prometheus.GaugeVec, requestDuration *prometheus.HistogramVec, auth authConfig, rateRPS float64, rateBurst int, tenantRateRPS float64, tenantRateBurst int, limits tenantLimits, status *statusTracker, meshNode *mesh.Node, anomalyState *meshAnomalyState, agentReg *agent.Registry, corsCfg corsConfig, enableAccessLog bool, eventBroker *events.Broker, hookDispatcher *webhook.Dispatcher, blockSigningKey string) http.Handler {
	mux := http.NewServeMux()
	getState := func(r *http.Request) (*tenantState, string, error) {
		tenant, ok := tenantFromContext(r.Context())
		if !ok || strings.TrimSpace(tenant) == "" {
			tenant = auth.defaultTenant
		}
		state, err := tenantMgr.getTenant(tenant)
		if err != nil {
			return nil, "", err
		}
		return state, tenant, nil
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := healthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Blocks:    state.chain.Height(),
			Routes:    state.router.RouteCount(),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// Admin access is enforced in auth middleware.
		w.Header().Set("Content-Type", "application/json")
		if status == nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(status.snapshot())
	})

	mux.HandleFunc("/admin/mesh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if meshNode == nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "disabled"})
			return
		}
		stats, cfg := meshNode.Snapshot()
		anomaly := map[string]any{}
		if anomalyState != nil {
			anomalyState.mu.Lock()
			if anomalyState.lastAt != nil {
				anomaly["last_at"] = anomalyState.lastAt.UTC().Format(time.RFC3339)
				anomaly["reason"] = anomalyState.lastReason
			}
			anomaly["reject_rate"] = anomalyState.lastRejectRate
			anomaly["received"] = anomalyState.lastReceived
			anomalyState.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"config": map[string]any{
				"node_id":            cfg.NodeID,
				"bind_addr":          cfg.BindAddr,
				"peers":              cfg.Peers,
				"heartbeat_interval": cfg.HeartbeatInterval.String(),
				"route_ttl":          cfg.RouteTTL.String(),
				"shared_keys":        len(cfg.SharedKeys),
				"peer_key_overrides": len(cfg.PeerKeys),
				"tenant_id":          cfg.TenantID,
			},
			"stats":   stats,
			"anomaly": anomaly,
		})
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := db.Ping(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		names, err := db.TenantNames()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tenants")
			return
		}
		if auth.defaultTenant != "" && !stringInSlice(auth.defaultTenant, names) {
			names = append(names, auth.defaultTenant)
		}
		sort.Strings(names)
		type tenantSummary struct {
			Tenant string `json:"tenant"`
			Blocks int    `json:"blocks"`
			Routes int    `json:"routes"`
		}
		out := make([]tenantSummary, 0, len(names))
		for _, name := range names {
			state, err := tenantMgr.getTenant(name)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			out = append(out, tenantSummary{
				Tenant: name,
				Blocks: state.chain.Height(),
				Routes: state.router.RouteCount(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.Handle("/metrics", promhttp.Handler())

	if meshNode != nil && meshPackets != nil {
		window := getEnvInt("MESH_ANOMALY_WINDOW", 5)
		rejectRateThreshold := getEnvFloat("MESH_REJECT_RATE_THRESHOLD", 0.3)
		rejectBurstThreshold := getEnvInt("MESH_REJECT_BURST_THRESHOLD", 20)
		minSamples := getEnvInt("MESH_ANOMALY_MIN_SAMPLES", 50)
		go updateMeshMetrics(meshNode, meshPackets, meshRejectRate, meshAnomaly, anomalyState, 2*time.Second, window, rejectRateThreshold, rejectBurstThreshold, minSamples)
	}

	mux.HandleFunc("/blockchain/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		if limits.maxBlocks > 0 && state.chain.Height() >= limits.maxBlocks {
			writeError(w, http.StatusTooManyRequests, "tenant block limit reached")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var txs []blockchain.Transaction
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&txs); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if err := validateTransactions(txs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		block := state.chain.AddSignedBlock(txs, blockSigningKey)
		if err := db.SaveChainTenant(tenant, state.chain); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist blockchain")
			return
		}
		if eventBroker != nil {
			eventBroker.Publish(events.Event{
				Type:     events.EventBlockAdded,
				TenantID: tenant,
				Data:     block,
			})
		}
		if hookDispatcher != nil {
			hookDispatcher.Dispatch(r.Context(), events.Event{
				Type:     events.EventBlockAdded,
				TenantID: tenant,
				Data:     block,
			})
		}
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("/proto/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid protobuf body")
			return
		}
		var msg meshpb.DataMessage
		if err := proto.Unmarshal(raw, &msg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid protobuf body")
			return
		}
		if msg.MsgType != meshpb.DataMessage_DATA {
			writeError(w, http.StatusBadRequest, "invalid message type")
			return
		}
		if msg.TenantId != "" && msg.TenantId != tenant {
			writeError(w, http.StatusForbidden, "tenant mismatch")
			return
		}
		ack := &meshpb.Ack{
			SourceId:      msg.DestinationId,
			DestinationId: msg.SourceId,
			Timestamp:     time.Now().UTC().Unix(),
			Message:       "ack",
			TenantId:      tenant,
		}
		out, err := proto.Marshal(ack)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	})

	mux.HandleFunc("/proto/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid protobuf body")
			return
		}
		var msg meshpb.ControlMessage
		if err := proto.Unmarshal(raw, &msg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid protobuf body")
			return
		}
		if msg.TenantId != "" && msg.TenantId != tenant {
			writeError(w, http.StatusForbidden, "tenant mismatch")
			return
		}
		ack := &meshpb.Ack{
			SourceId:      msg.NodeId,
			DestinationId: msg.NodeId,
			Timestamp:     time.Now().UTC().Unix(),
			Message:       "ack",
			TenantId:      tenant,
		}
		out, err := proto.Marshal(ack)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode response")
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(out)
	})

	mux.HandleFunc("/routes", func(w http.ResponseWriter, r *http.Request) {
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if limits.maxRoutes > 0 && state.router.RouteCount() >= limits.maxRoutes {
				writeError(w, http.StatusTooManyRequests, "tenant route limit reached")
				return
			}
			dest := r.URL.Query().Get("destination")
			lat, ok, err := parseIntQuery(r, "latency")
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !ok {
				lat = 0
			}
			thr, ok, err := parseIntQuery(r, "throughput")
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !ok {
				thr = 0
			}
			ttlSeconds, _, err := parseIntQueryMulti(r, "ttlSeconds", "ttl_seconds")
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if ttlSeconds < 0 {
				writeError(w, http.StatusBadRequest, "ttlSeconds must be non-negative")
				return
			}
			var ttl time.Duration
			if ttlSeconds > 0 {
				ttl = time.Duration(ttlSeconds) * time.Second
			}
			satellite := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("satellite"))) == "true"
			tags := parseTags(r.URL.Query()["tag"])
			if err := state.router.AddRouteWithOptions(dest, routing.RouteMetric{
				Latency:    lat,
				Throughput: thr,
			}, routing.RouteOptions{
				TTL:       ttl,
				Satellite: satellite,
				Tags:      tags,
			}); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := db.SaveRoutesTenant(tenant, state.router); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to persist routes")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventRouteAdded,
					TenantID: tenant,
					Data:     map[string]any{"destination": dest, "satellite": satellite},
				})
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			dest := strings.TrimSpace(r.URL.Query().Get("destination"))
			if dest == "" {
				writeError(w, http.StatusBadRequest, "destination is required")
				return
			}
			if err := state.router.DeleteRoute(dest); err != nil {
				writeError(w, http.StatusNotFound, "route not found")
				return
			}
			if err := db.SaveRoutesTenant(tenant, state.router); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to persist routes")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventRouteDeleted,
					TenantID: tenant,
					Data:     map[string]string{"destination": dest},
				})
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			tagFilter := parseTags(r.URL.Query()["tag"])
			if len(tagFilter) > 0 {
				_ = json.NewEncoder(w).Encode(state.router.FilterByTags(tagFilter))
			} else {
				_ = json.NewEncoder(w).Encode(state.router.ListRoutes())
			}
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/routes/best", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		best, err := state.router.SelectBestNextHop()
		if err != nil {
			writeError(w, http.StatusNotFound, "no routes available")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(best)
	})

	mux.HandleFunc("/blockchain", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		height := state.chain.Height()
		limit, _, err := parseIntQueryMulti(r, "limit")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		offset, _, err := parseIntQueryMulti(r, "offset")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if limit <= 0 || limit > 1000 {
			limit = 100
		}
		if offset < 0 {
			offset = 0
		}
		blocks := state.chain.BlockRange(offset, offset+limit)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"height": height,
			"offset": offset,
			"limit":  limit,
			"blocks": blocks,
		})
	})

	mux.HandleFunc("/blockchain/height", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"height": state.chain.Height()})
	})

	defaultAgentTTL := getEnvInt("AGENT_TTL_SECONDS", 300)

	mux.HandleFunc("/agents", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			capability := strings.TrimSpace(r.URL.Query().Get("capability"))
			var agents []agent.Agent
			if capability != "" {
				agents = agentReg.ListByCapability(tenant, capability)
			} else {
				agents = agentReg.List(tenant)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(agents)
		case http.MethodDelete:
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			if id == "" {
				writeError(w, http.StatusBadRequest, "id is required")
				return
			}
			if err := agentReg.Deregister(tenant, id); err != nil {
				writeError(w, http.StatusNotFound, "agent not found")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/agents/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
		var req agent.RegisterRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		// Tenant is always taken from the authenticated context; the caller
		// cannot override it.
		req.TenantID = tenant
		if req.TTLSeconds == 0 {
			req.TTLSeconds = defaultAgentTTL
		}
		if err := agentReg.Register(req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a, _ := agentReg.Get(tenant, req.ID)
		if eventBroker != nil {
			eventBroker.Publish(events.Event{
				Type:     events.EventAgentRegistered,
				TenantID: tenant,
				Data:     a,
			})
		}
		if hookDispatcher != nil {
			hookDispatcher.Dispatch(r.Context(), events.Event{
				Type:     events.EventAgentRegistered,
				TenantID: tenant,
				Data:     a,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(a)
	})

	mux.HandleFunc("/agents/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeError(w, http.StatusBadRequest, "id is required")
			return
		}
		ttl, _, _ := parseIntQueryMulti(r, "ttl_seconds")
		if ttl <= 0 {
			ttl = defaultAgentTTL
		}
		if err := agentReg.Heartbeat(tenant, id, ttl); err != nil {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /blockchain/{index} — fetch a single block by index.
	// Registered as a subtree pattern ("/blockchain/") so it catches paths
	// not matched by the more-specific "/blockchain/add", "/blockchain/height".
	mux.HandleFunc("/blockchain/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// Strip prefix and parse the index segment.
		suffix := strings.TrimPrefix(r.URL.Path, "/blockchain/")
		suffix = strings.TrimSuffix(suffix, "/")
		if suffix == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		idx, err := strconv.Atoi(suffix)
		if err != nil || idx < 0 {
			writeError(w, http.StatusBadRequest, "invalid block index")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		block, ok := state.chain.GetBlock(idx)
		if !ok {
			writeError(w, http.StatusNotFound, "block not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(block)
	})

	// GET /events — Server-Sent Events stream for real-time event fan-out.
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if eventBroker == nil {
			writeError(w, http.StatusServiceUnavailable, "event broker unavailable")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		subID := r.Header.Get("X-Request-ID")
		if subID == "" {
			subID = "sse-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}

		ch := eventBroker.Subscribe(subID, 64)
		defer eventBroker.Unsubscribe(subID)

		// Send a connected confirmation event.
		_, _ = io.WriteString(w, "event: connected\ndata: {\"subscriber_id\":\""+subID+"\"}\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case ev, open := <-ch:
				if !open {
					return
				}
				// Filter by tenant (skip if tenant set and event is for a different one).
				if strings.TrimSpace(tenant) != "" && ev.TenantID != "" && ev.TenantID != tenant {
					continue
				}
				// Filter by event type if requested.
				if typeFilter != "" && string(ev.Type) != typeFilter {
					continue
				}
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("spectral-cloud running\n"))
	})

	var limiter *ipLimiter
	if rateRPS > 0 && rateBurst > 0 {
		limiter = newIPLimiter(rate.Limit(rateRPS), rateBurst, 5*time.Minute)
	}
	var tenantLimiter *tenantLimiter
	if tenantRateRPS > 0 && tenantRateBurst > 0 {
		tenantLimiter = newTenantLimiter(rate.Limit(tenantRateRPS), tenantRateBurst, 10*time.Minute)
	}
	handler := withAuth(mux, auth)
	handler = withTenantRateLimit(handler, tenantLimiter, auth.defaultTenant)
	handler = withRateLimit(handler, limiter)
	handler = withMetrics(handler, requestsTotal)
	handler = withDuration(handler, requestDuration)
	handler = withCORS(handler, corsCfg)
	if enableAccessLog {
		handler = withLogger(handler)
	}
	handler = withRequestID(handler)
	handler = withRecover(handler)
	return handler
}

func withMetrics(next http.Handler, counter *prometheus.CounterVec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		_ = start
		counter.WithLabelValues(r.URL.Path, r.Method, strconv.Itoa(rec.status)).Inc()
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func parseIntQuery(r *http.Request, key string) (int, bool, error) {
	val := strings.TrimSpace(r.URL.Query().Get(key))
	if val == "" {
		return 0, false, nil
	}
	out, err := strconv.Atoi(val)
	if err != nil {
		return 0, true, errors.New("invalid " + key)
	}
	return out, true, nil
}

func parseIntQueryMulti(r *http.Request, keys ...string) (int, bool, error) {
	for _, key := range keys {
		if v, ok, err := parseIntQuery(r, key); ok || err != nil {
			return v, ok, err
		}
	}
	return 0, false, nil
}

func getEnvInt(key string, def int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return parsed
}

func getEnvFloat(key string, def float64) float64 {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return def
	}
	return parsed
}

func getEnvBool(key string, def bool) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if val == "" {
		return def
	}
	if val == "1" || val == "true" || val == "yes" || val == "y" {
		return true
	}
	if val == "0" || val == "false" || val == "no" || val == "n" {
		return false
	}
	return def
}

func getEnvUint32(key string, def uint32) uint32 {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseUint(val, 10, 32)
	if err != nil {
		return def
	}
	return uint32(parsed)
}

func loadMeshConfig() (mesh.Config, error) {
	bind := strings.TrimSpace(os.Getenv("MESH_UDP_BIND"))
	if bind == "" {
		bind = "0.0.0.0:7000"
	}
	nodeID := getEnvUint32("MESH_NODE_ID", 0)
	if nodeID == 0 {
		nodeID = randomNodeID()
	}
	peersRaw := strings.TrimSpace(os.Getenv("MESH_PEERS"))
	var peers []string
	if peersRaw != "" {
		parts := strings.Split(peersRaw, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			peers = append(peers, part)
		}
	}
	heartbeat := strings.TrimSpace(os.Getenv("MESH_HEARTBEAT_INTERVAL"))
	routeTTL := strings.TrimSpace(os.Getenv("MESH_ROUTE_TTL"))
	sharedKey := strings.TrimSpace(os.Getenv("MESH_SHARED_KEY"))
	sharedKeysRaw := strings.TrimSpace(os.Getenv("MESH_SHARED_KEYS"))
	sharedKeys := parseKeyList(sharedKeysRaw)
	if sharedKey != "" {
		sharedKeys = append([]string{sharedKey}, sharedKeys...)
	}
	peerKeys, err := parsePeerKeys(strings.TrimSpace(os.Getenv("MESH_PEER_KEYS")))
	if err != nil {
		return mesh.Config{}, err
	}
	interval := 5 * time.Second
	if heartbeat != "" {
		if d, err := time.ParseDuration(heartbeat); err == nil && d > 0 {
			interval = d
		}
	}
	ttl := 30 * time.Second
	if routeTTL != "" {
		if d, err := time.ParseDuration(routeTTL); err == nil && d > 0 {
			ttl = d
		}
	}
	gossipIntervalRaw := strings.TrimSpace(os.Getenv("MESH_GOSSIP_INTERVAL"))
	gossipInterval := time.Duration(0) // 0 = disabled
	if gossipIntervalRaw != "" {
		if d, err := time.ParseDuration(gossipIntervalRaw); err == nil && d > 0 {
			gossipInterval = d
		}
	}
	gossipMaxRoutes := getEnvInt("MESH_GOSSIP_MAX_ROUTES", 50)

	return mesh.Config{
		NodeID:            nodeID,
		BindAddr:          bind,
		Peers:             peers,
		HeartbeatInterval: interval,
		RouteTTL:          ttl,
		SharedKeys:        sharedKeys,
		PeerKeys:          peerKeys,
		GossipInterval:    gossipInterval,
		GossipMaxRoutes:   gossipMaxRoutes,
	}, nil
}

func randomNodeID() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func parseKeyList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func parsePeerKeys(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	out := make(map[string]string)
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, "=", 2)
		if len(pieces) != 2 {
			return nil, errors.New("invalid MESH_PEER_KEYS entry")
		}
		peer := strings.TrimSpace(pieces[0])
		key := strings.TrimSpace(pieces[1])
		if peer == "" || key == "" {
			return nil, errors.New("invalid MESH_PEER_KEYS entry")
		}
		out[peer] = key
	}
	return out, nil
}

// parseTags converts a slice of "key:value" strings into a map.
// Entries that are not in "key:value" format are silently skipped.
func parseTags(raw []string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		parts := strings.SplitN(kv, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateTransactions(txs []blockchain.Transaction) error {
	for i, tx := range txs {
		if strings.TrimSpace(tx.Sender) == "" || strings.TrimSpace(tx.Recipient) == "" {
			return errors.New("transaction sender and recipient must be set")
		}
		if tx.Amount < 0 {
			return errors.New("transaction amount must be non-negative")
		}
		if i >= 1000 {
			return errors.New("too many transactions")
		}
	}
	return nil
}

func migrateLegacyJSON(dataDir string, db *store.Store, tenant string, backupRetention int) (bool, error) {
	chainPath := filepath.Join(dataDir, "blockchain.json")
	routesPath := filepath.Join(dataDir, "routes.json")

	blocks, blocksLoaded, err := loadLegacyBlocks(chainPath, backupRetention)
	if err != nil {
		return false, err
	}
	routes, routesLoaded, err := loadLegacyRoutes(routesPath, backupRetention)
	if err != nil {
		return false, err
	}
	if !blocksLoaded && !routesLoaded {
		return false, nil
	}
	if blocksLoaded {
		valid := filterValidBlocks(blocks)
		if len(valid) == 0 {
			valid = blockchain.NewBlockchain().Snapshot()
		}
		if err := db.WriteBlocksTenant(tenant, valid); err != nil {
			return false, err
		}
		_ = renameLegacy(chainPath)
	}
	if routesLoaded {
		pruned := pruneExpiredRoutes(routes)
		if err := db.WriteRoutesTenant(tenant, pruned); err != nil {
			return false, err
		}
		_ = renameLegacy(routesPath)
	}
	return true, nil
}

func renameLegacy(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	return os.Rename(path, path+"."+ts+".migrated")
}

func loadLegacyBlocks(path string, backupRetention int) ([]blockchain.Block, bool, error) {
	if backedUp, err := backupFile(path); err != nil {
		return nil, false, err
	} else if backedUp {
		_ = rotateBackups(path, backupRetention)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var file blockchainFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Blocks) > 0 {
		return file.Blocks, true, nil
	}
	var blocks []blockchain.Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, false, err
	}
	if len(blocks) == 0 {
		return nil, false, nil
	}
	return blocks, true, nil
}

func loadLegacyRoutes(path string, backupRetention int) ([]routing.Route, bool, error) {
	if backedUp, err := backupFile(path); err != nil {
		return nil, false, err
	} else if backedUp {
		_ = rotateBackups(path, backupRetention)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var file routesFile
	if err := json.Unmarshal(data, &file); err == nil && len(file.Routes) > 0 {
		return file.Routes, true, nil
	}
	var routes []routing.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, false, err
	}
	if len(routes) == 0 {
		return nil, false, nil
	}
	return routes, true, nil
}

func loadFromStore(db *store.Store, tenant string, chain *blockchain.Blockchain, router *routing.RoutingEngine) error {
	blocks, err := db.ReadBlocksTenant(tenant)
	if err != nil {
		return err
	}
	if len(blocks) > 0 {
		valid := filterValidBlocks(blocks)
		chain.Load(valid)
		if len(valid) != len(blocks) {
			_ = db.WriteBlocksTenant(tenant, valid)
		}
	}
	routes, err := db.ReadRoutesTenant(tenant)
	if err != nil {
		return err
	}
	if len(routes) > 0 {
		pruned := pruneExpiredRoutes(routes)
		router.Load(pruned)
		if len(pruned) != len(routes) {
			_ = db.WriteRoutesTenant(tenant, pruned)
		}
	}
	return nil
}

func backupFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	backupPath := path + "." + ts + ".bak"
	input, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(backupPath, input, info.Mode()); err != nil {
		return false, err
	}
	return true, nil
}

func rotateBackups(path string, retain int) error {
	if retain <= 0 {
		return nil
	}
	pattern := path + ".*.bak"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(matches) <= retain {
		return nil
	}
	sort.Strings(matches)
	toDelete := matches[:len(matches)-retain]
	for _, file := range toDelete {
		_ = os.Remove(file)
	}
	return nil
}

func filterValidBlocks(blocks []blockchain.Block) []blockchain.Block {
	out := make([]blockchain.Block, 0, len(blocks))
	var previousHash string
	for _, block := range blocks {
		if !blockchain.Verify(&block) {
			break
		}
		if block.Index > 0 && block.PreviousHash != previousHash {
			break
		}
		previousHash = block.Hash
		out = append(out, block)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pruneExpiredRoutes(routes []routing.Route) []routing.Route {
	now := time.Now().UTC()
	out := routes[:0]
	for _, route := range routes {
		if route.ExpiresAt != nil && now.After(*route.ExpiresAt) {
			continue
		}
		out = append(out, route)
	}
	return out
}

func runBackupScheduler(ctx context.Context, dbPath, backupDir string, interval time.Duration, retention int, status *statusTracker) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := runBackup(dbPath, backupDir, retention, status); err != nil {
				log.Printf("scheduled backup failed: %v", err)
			}
		}
	}
}

func runCompactScheduler(ctx context.Context, dbPath, compactDir string, interval time.Duration, retention int, status *statusTracker) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := runCompaction(dbPath, compactDir, retention, status); err != nil {
				log.Printf("scheduled compaction failed: %v", err)
			}
		}
	}
}

func runBackup(dbPath, backupDir string, retention int, status *statusTracker) error {
	base := filepath.Join(backupDir, filepath.Base(dbPath))
	ts := time.Now().UTC().Format("20060102T150405Z")
	outPath := base + "." + ts + ".bak"
	key := strings.TrimSpace(os.Getenv("BACKUP_KEY_B64"))
	if key == "" {
		if err := store.Backup(dbPath, outPath); err != nil {
			if status != nil {
				status.recordBackup(err)
			}
			return err
		}
		if err := store.Verify(outPath); err != nil {
			if status != nil {
				status.recordBackup(err)
			}
			return err
		}
	} else {
		if err := store.BackupEncrypted(dbPath, outPath, key); err != nil {
			if status != nil {
				status.recordBackup(err)
			}
			return err
		}
		if err := store.VerifyEncrypted(outPath, key); err != nil {
			if status != nil {
				status.recordBackup(err)
			}
			return err
		}
	}
	if err := rotateBackups(base, retention); err != nil {
		if status != nil {
			status.recordBackup(err)
		}
		return err
	}
	if status != nil {
		status.recordBackup(nil)
	}
	return nil
}

func runCompaction(dbPath, compactDir string, retention int, status *statusTracker) error {
	base := filepath.Join(compactDir, filepath.Base(dbPath))
	ts := time.Now().UTC().Format("20060102T150405Z")
	outPath := base + "." + ts + ".compact"
	if err := store.Compact(dbPath, outPath); err != nil {
		if status != nil {
			status.recordCompact(err)
		}
		return err
	}
	if err := store.Verify(outPath); err != nil {
		if status != nil {
			status.recordCompact(err)
		}
		return err
	}
	if err := rotateBackups(base, retention); err != nil {
		if status != nil {
			status.recordCompact(err)
		}
		return err
	}
	if status != nil {
		status.recordCompact(nil)
	}
	return nil
}

func updateMeshMetrics(node *mesh.Node, counter *prometheus.CounterVec, rejectRateGauge prometheus.Gauge, anomalyGauge *prometheus.GaugeVec, state *meshAnomalyState, interval time.Duration, window int, rejectRateThreshold float64, rejectBurstThreshold int, minSamples int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last mesh.Stats
	rejectRates := make([]float64, 0, window)
	for range ticker.C {
		stats, _ := node.Snapshot()
		if stats.Received > last.Received {
			counter.WithLabelValues("received").Add(float64(stats.Received - last.Received))
		}
		if stats.Accepted > last.Accepted {
			counter.WithLabelValues("accepted").Add(float64(stats.Accepted - last.Accepted))
		}
		if stats.Rejected > last.Rejected {
			counter.WithLabelValues("rejected").Add(float64(stats.Rejected - last.Rejected))
		}
		if stats.RejectedAuth > last.RejectedAuth {
			counter.WithLabelValues("rejected_auth").Add(float64(stats.RejectedAuth - last.RejectedAuth))
		}
		if stats.RejectedParse > last.RejectedParse {
			counter.WithLabelValues("rejected_parse").Add(float64(stats.RejectedParse - last.RejectedParse))
		}
		receivedDelta := int64(stats.Received - last.Received)
		rejectedDelta := int64(stats.Rejected - last.Rejected)
		if receivedDelta > 0 {
			rate := float64(rejectedDelta) / float64(receivedDelta)
			rejectRateGauge.Set(rate)
			rejectRates = append(rejectRates, rate)
			if window > 0 && len(rejectRates) > window {
				rejectRates = rejectRates[len(rejectRates)-window:]
			}
			triggered, reason := detectAnomaly(rate, rejectedDelta, receivedDelta, rejectRateThreshold, rejectBurstThreshold, minSamples, rejectRates)
			if anomalyGauge != nil {
				if triggered {
					anomalyGauge.WithLabelValues("reject_rate").Set(1)
				} else {
					anomalyGauge.WithLabelValues("reject_rate").Set(0)
				}
			}
			if state != nil {
				state.mu.Lock()
				state.lastRejectRate = rate
				state.lastReceived = uint64(receivedDelta)
				if triggered {
					now := time.Now().UTC()
					state.lastAt = &now
					state.lastReason = reason
				}
				state.mu.Unlock()
			}
		}
		last = stats
	}
}

func detectAnomaly(rejectRate float64, rejectedDelta, receivedDelta int64, rateThreshold float64, burstThreshold int, minSamples int, window []float64) (bool, string) {
	if receivedDelta < int64(minSamples) {
		return false, ""
	}
	if burstThreshold > 0 && rejectedDelta >= int64(burstThreshold) {
		return true, "reject_burst"
	}
	if rateThreshold > 0 && rejectRate >= rateThreshold {
		return true, "reject_rate"
	}
	// Simple trend: if last 3 rates are increasing and above half threshold.
	if len(window) >= 3 && rateThreshold > 0 {
		n := len(window)
		if window[n-3] < window[n-2] && window[n-2] < window[n-1] && window[n-1] >= rateThreshold*0.5 {
			return true, "reject_rate_trend"
		}
	}
	return false, ""
}

func withAuth(next http.Handler, auth authConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if matchPathRules(path, r.Method, auth.publicRules) {
			next.ServeHTTP(w, r)
			return
		}
		if matchPathRules(path, r.Method, auth.adminRules) {
			if strings.TrimSpace(auth.adminKey) == "" && strings.TrimSpace(auth.apiKey) == "" {
				writeError(w, http.StatusForbidden, "admin endpoint requires API key")
				return
			}
			if !adminIPAllowed(r, auth.adminCIDRs, auth.allowRemote) {
				writeError(w, http.StatusForbidden, "admin endpoint is local-only")
				return
			}
			key := auth.adminKey
			if isWriteMethod(r.Method) && strings.TrimSpace(auth.adminWriteKey) != "" {
				key = auth.adminWriteKey
			}
			if strings.TrimSpace(key) == "" {
				key = auth.apiKey
			}
			if !hasValidAPIKey(r, key) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if auth.tenantKeys != nil {
			manager := auth.tenantKeys
			if isWriteMethod(r.Method) && auth.tenantWrite != nil {
				manager = auth.tenantWrite
			}
			tenant, ok := manager.TenantFromRequest(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			r = r.WithContext(withTenant(r.Context(), tenant))
			next.ServeHTTP(w, r)
			return
		}
		if isWriteMethod(r.Method) && strings.TrimSpace(auth.writeKey) != "" {
			if !hasValidAPIKey(r, auth.writeKey) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if auth.defaultTenant != "" {
				r = r.WithContext(withTenant(r.Context(), auth.defaultTenant))
			}
		} else if strings.TrimSpace(auth.apiKey) != "" {
			if !hasValidAPIKey(r, auth.apiKey) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if auth.defaultTenant != "" {
				r = r.WithContext(withTenant(r.Context(), auth.defaultTenant))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func hasValidAPIKey(r *http.Request, apiKey string) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	token := ""
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		token = strings.TrimSpace(header[7:])
	}
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-API-Key"))
	}
	if token == "" {
		return false
	}
	return secureEquals(token, apiKey)
}

func isLocalRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func adminIPAllowed(r *http.Request, allowlist []*net.IPNet, allowRemote bool) bool {
	ip := clientIP(r)
	if ip == nil {
		return false
	}
	if len(allowlist) > 0 {
		for _, n := range allowlist {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if allowRemote {
		return true
	}
	return ip.IsLoopback()
}

func clientIP(r *http.Request) net.IP {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	return net.ParseIP(host)
}

func stringInSlice(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func parseCIDRList(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, netblock, err := net.ParseCIDR(part)
		if err != nil {
			return nil, err
		}
		out = append(out, netblock)
	}
	return out, nil
}

func parsePathRules(raw string, defaults []string) []pathRule {
	value := strings.TrimSpace(raw)
	var parts []string
	if value == "" {
		parts = defaults
	} else {
		parts = strings.Split(value, ",")
	}
	out := make([]pathRule, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		rule, ok := parsePathRule(part)
		if !ok {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func matchPathRules(path, method string, rules []pathRule) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, rule := range rules {
		if rule.method != "" && rule.method != method {
			continue
		}
		if rule.prefix {
			if strings.HasPrefix(path, rule.value) {
				return true
			}
			continue
		}
		if path == rule.value {
			return true
		}
	}
	return false
}

func parsePathRule(part string) (pathRule, bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return pathRule{}, false
	}
	method := ""
	path := part
	if strings.Contains(part, " ") {
		pieces := strings.Fields(part)
		if len(pieces) >= 2 && isHTTPMethod(pieces[0]) {
			method = strings.ToUpper(pieces[0])
			path = pieces[1]
		}
	} else if strings.Contains(part, ":") {
		pieces := strings.SplitN(part, ":", 2)
		if len(pieces) == 2 && isHTTPMethod(pieces[0]) {
			method = strings.ToUpper(pieces[0])
			path = pieces[1]
		}
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return pathRule{}, false
	}
	if strings.HasSuffix(path, "*") {
		return pathRule{value: strings.TrimSuffix(path, "*"), prefix: true, method: method}, true
	}
	return pathRule{value: path, prefix: false, method: method}, true
}

func isHTTPMethod(val string) bool {
	switch strings.ToUpper(strings.TrimSpace(val)) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func isWriteMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func secureEquals(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var out byte
	for i := 0; i < len(a); i++ {
		out |= a[i] ^ b[i]
	}
	return out == 0
}

type ipLimiter struct {
	limit   rate.Limit
	burst   int
	ttl     time.Duration
	clients map[string]*clientLimiter
	lastGC  time.Time
	mu      sync.Mutex
}

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPLimiter(limit rate.Limit, burst int, ttl time.Duration) *ipLimiter {
	return &ipLimiter{
		limit:   limit,
		burst:   burst,
		ttl:     ttl,
		clients: make(map[string]*clientLimiter),
		lastGC:  time.Now().UTC(),
	}
}

func (l *ipLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(l.lastGC) > l.ttl {
		for k, v := range l.clients {
			if now.Sub(v.lastSeen) > l.ttl {
				delete(l.clients, k)
			}
		}
		l.lastGC = now
	}
	if c, ok := l.clients[ip]; ok {
		c.lastSeen = now
		return c.limiter
	}
	lim := rate.NewLimiter(l.limit, l.burst)
	l.clients[ip] = &clientLimiter{limiter: lim, lastSeen: now}
	return lim
}

func withRateLimit(next http.Handler, limiter *ipLimiter) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			ip = host
		}
		if !limiter.get(ip).Allow() {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type tenantLimiter struct {
	limit   rate.Limit
	burst   int
	mu      sync.Mutex
	entries map[string]*tenantEntry
	ttl     time.Duration
}

type tenantEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newTenantLimiter(limit rate.Limit, burst int, ttl time.Duration) *tenantLimiter {
	return &tenantLimiter{
		limit:   limit,
		burst:   burst,
		entries: map[string]*tenantEntry{},
		ttl:     ttl,
	}
}

func (l *tenantLimiter) get(tenant string) *rate.Limiter {
	if strings.TrimSpace(tenant) == "" {
		return nil
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := l.entries[tenant]; ok {
		entry.lastSeen = now
		return entry.limiter
	}
	l.cleanup(now)
	lim := rate.NewLimiter(l.limit, l.burst)
	l.entries[tenant] = &tenantEntry{limiter: lim, lastSeen: now}
	return lim
}

func (l *tenantLimiter) cleanup(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.lastSeen) > l.ttl {
			delete(l.entries, key)
		}
	}
}

func withTenantRateLimit(next http.Handler, limiter *tenantLimiter, defaultTenant string) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := tenantFromContext(r.Context())
		if !ok || strings.TrimSpace(tenant) == "" {
			tenant = defaultTenant
		}
		lim := limiter.get(tenant)
		if lim != nil && !lim.Allow() {
			writeError(w, http.StatusTooManyRequests, "tenant rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
