package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/agent"
	"github.com/gdev6145/Spectral_cloud/pkg/agentgroup"
	"github.com/gdev6145/Spectral_cloud/pkg/pipeline"
	"github.com/gdev6145/Spectral_cloud/pkg/auth"
	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/circuit"
	"github.com/gdev6145/Spectral_cloud/pkg/events"
	"github.com/gdev6145/Spectral_cloud/pkg/healthcheck"
	"github.com/gdev6145/Spectral_cloud/pkg/jobs"
	"github.com/gdev6145/Spectral_cloud/pkg/kv"
	"github.com/gdev6145/Spectral_cloud/pkg/ai"
	"github.com/gdev6145/Spectral_cloud/pkg/billing"
	"github.com/gdev6145/Spectral_cloud/pkg/mesh"
	"github.com/gdev6145/Spectral_cloud/pkg/notify"
	meshpb "github.com/gdev6145/Spectral_cloud/pkg/proto"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/scheduler"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
	"github.com/gdev6145/Spectral_cloud/pkg/webhook"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/proto"
)

//go:embed static
var staticFiles embed.FS

type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Blocks    int    `json:"blocks"`
	Routes    int    `json:"routes"`
}

type authWhoAmIResponse struct {
	Authenticated bool   `json:"authenticated"`
	Access        string `json:"access"`
	Tenant        string `json:"tenant,omitempty"`
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
	dbPath              string
	dataDir             string
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

// ── in-memory timeseries metrics buffer ────────────────────────────────────

type metricsPoint struct {
	TS         string `json:"ts"`
	Blocks     int    `json:"blocks"`
	Routes     int    `json:"routes"`
	Agents     int    `json:"agents"`
	JobsQueued int    `json:"jobs_queued"`
	Peers      int    `json:"peers"`
}

type metricsBuffer struct {
	mu     sync.Mutex
	buf    []metricsPoint
	maxLen int
}

func newMetricsBuf(size int) *metricsBuffer {
	return &metricsBuffer{maxLen: size, buf: make([]metricsPoint, 0, size)}
}

func (b *metricsBuffer) push(p metricsPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p)
	if len(b.buf) > b.maxLen {
		b.buf = b.buf[len(b.buf)-b.maxLen:]
	}
}

func (b *metricsBuffer) last(n int) []metricsPoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := len(b.buf)
	if n <= 0 || n > l {
		n = l
	}
	result := make([]metricsPoint, n)
	copy(result, b.buf[l-n:])
	return result
}

// globalMetricsBuf is populated by a background goroutine started in main().
var globalMetricsBuf = newMetricsBuf(720)

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
	loadFileConfig()
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

	tenantBlockHeight := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "spectral_cloud_tenant_blockchain_height",
			Help: "Current blockchain height per tenant.",
		},
		[]string{"tenant"},
	)
	prometheus.MustRegister(tenantBlockHeight)

	tenantRouteCount := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "spectral_cloud_tenant_route_count",
			Help: "Current route count per tenant.",
		},
		[]string{"tenant"},
	)
	prometheus.MustRegister(tenantRouteCount)

	tenantAgentCount := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "spectral_cloud_tenant_agent_count",
			Help: "Current registered agent count per tenant.",
		},
		[]string{"tenant"},
	)
	prometheus.MustRegister(tenantAgentCount)

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
		dbPath:              dbPath,
		dataDir:             dataDir,
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

	jobQueue := jobs.NewQueueWithStore(db)
	go func() {
		// Prune completed jobs older than 24h every 10 minutes.
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				jobQueue.Prune(24 * time.Hour)
			}
		}
	}()

	eventBroker := events.NewBroker()

	var hcChecker *healthcheck.Checker
	hcIntervalStr := strings.TrimSpace(os.Getenv("AGENT_HEALTH_INTERVAL"))
	if hcIntervalStr != "" {
		if hcInterval, err := time.ParseDuration(hcIntervalStr); err == nil && hcInterval > 0 {
			hcTimeout := 5 * time.Second
			if t, err := time.ParseDuration(strings.TrimSpace(os.Getenv("AGENT_HEALTH_TIMEOUT"))); err == nil && t > 0 {
				hcTimeout = t
			}
			hcChecker = healthcheck.New(agentReg, eventBroker, hcInterval, hcTimeout)
			hcChecker.Start(ctx)
			log.Printf("agent health checker enabled (interval=%s, timeout=%s)", hcInterval, hcTimeout)
		}
	}

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

	kvStore := kv.New()
	go func() {
		// Prune expired KV entries every 5 minutes.
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				kvStore.Prune()
			}
		}
	}()

	notifyMgr := notify.NewWithStore(db)

	circuitThreshold := getEnvInt("CIRCUIT_THRESHOLD", 5)
	circuitResetSec := getEnvInt("CIRCUIT_RESET_SECONDS", 30)
	circuitMgr := circuit.New(circuitThreshold, time.Duration(circuitResetSec)*time.Second)

	groupMgr := agentgroup.NewWithStore(db)

	pipelineMgr := pipeline.NewWithStore(db)

	meter := billing.New(db)
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for range tick.C {
			meter.Flush()
		}
	}()

	sched := scheduler.NewWithStore(jobQueue, db)
	defer sched.StopAll()

	// Restore jobs and schedules persisted in previous runs.
	if tenantNames, err := db.TenantNames(); err == nil {
		for _, tn := range tenantNames {
			if n, err := jobQueue.LoadFromStore(tn); err != nil {
				log.Printf("warn: failed to load jobs for tenant %q: %v", tn, err)
			} else if n > 0 {
				log.Printf("restored %d job(s) for tenant %q", n, tn)
			}
			if n, err := sched.LoadFromStore(ctx, tn); err != nil {
				log.Printf("warn: failed to load schedules for tenant %q: %v", tn, err)
			} else if n > 0 {
				log.Printf("restored %d schedule(s) for tenant %q", n, tn)
			}
			if n, err := groupMgr.LoadFromStore(tn); err != nil {
				log.Printf("warn: failed to load agent groups for tenant %q: %v", tn, err)
			} else if n > 0 {
				log.Printf("restored %d agent group(s) for tenant %q", n, tn)
			}
			if np, nr, err := pipelineMgr.LoadFromStore(tn); err != nil {
				log.Printf("warn: failed to load pipelines for tenant %q: %v", tn, err)
			} else if np > 0 || nr > 0 {
				log.Printf("restored %d pipeline(s), %d run(s) for tenant %q", np, nr, tn)
			}
			if n, err := notifyMgr.LoadFromStore(tn); err != nil {
				log.Printf("warn: failed to load notification rules for tenant %q: %v", tn, err)
			} else if n > 0 {
				log.Printf("restored %d notification rule(s) for tenant %q", n, tn)
			}
		}
	}
	notifyMgr.Start(ctx, eventBroker)

	corsCfg := parseCORSConfig(
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")),
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_METHODS")),
		strings.TrimSpace(os.Getenv("CORS_ALLOWED_HEADERS")),
		strings.TrimSpace(os.Getenv("CORS_MAX_AGE")),
	)
	enableAccessLog := getEnvBool("ACCESS_LOG", false)

	// Update per-tenant Prometheus gauges every 15 seconds.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				names, err := db.TenantNames()
				if err != nil {
					continue
				}
				if defaultTenant != "" && !stringInSlice(defaultTenant, names) {
					names = append(names, defaultTenant)
				}
				for _, name := range names {
					state, err := tenantMgr.getTenant(name)
					if err != nil {
						continue
					}
					tenantBlockHeight.WithLabelValues(name).Set(float64(state.chain.Height()))
					tenantRouteCount.WithLabelValues(name).Set(float64(state.router.RouteCount()))
					tenantAgentCount.WithLabelValues(name).Set(float64(agentReg.CountByTenant(name)))
				}
			}
		}
	}()

	// Sample system metrics every 5s into the global ring buffer.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state, err := tenantMgr.getTenant(defaultTenant)
				if err != nil {
					continue
				}
				var jobsQueued int
				if jobQueue != nil {
					jobsQueued = jobQueue.Count()
				}
				var peers int
				if meshNode != nil {
					peers = len(meshNode.PeerHealth())
				}
				globalMetricsBuf.push(metricsPoint{
					TS:         time.Now().UTC().Format(time.RFC3339),
					Blocks:     state.chain.Height(),
					Routes:     state.router.RouteCount(),
					Agents:     agentReg.CountByTenant(defaultTenant),
					JobsQueued: jobsQueued,
					Peers:      peers,
				})
			}
		}
	}()

	handler := newHandler(tenantMgr, db, maxBodyBytes, requestsTotal, meshPackets, meshRejectRate, meshAnomaly, requestDuration, auth, rateRPS, rateBurst, tenantRateRPS, tenantRateBurst, limits, status, meshNode, anomalyState, agentReg, corsCfg, enableAccessLog, eventBroker, hookDispatcher, blockSigningKey, jobQueue, hcChecker, kvStore, notifyMgr, circuitMgr, groupMgr, sched, pipelineMgr, meter)

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

func newHandler(tenantMgr *tenantManager, db *store.Store, maxBodyBytes int, requestsTotal *prometheus.CounterVec, meshPackets *prometheus.CounterVec, meshRejectRate prometheus.Gauge, meshAnomaly *prometheus.GaugeVec, requestDuration *prometheus.HistogramVec, auth authConfig, rateRPS float64, rateBurst int, tenantRateRPS float64, tenantRateBurst int, limits tenantLimits, status *statusTracker, meshNode *mesh.Node, anomalyState *meshAnomalyState, agentReg *agent.Registry, corsCfg corsConfig, enableAccessLog bool, eventBroker *events.Broker, hookDispatcher *webhook.Dispatcher, blockSigningKey string, jobQueue *jobs.Queue, hcChecker *healthcheck.Checker, kvStore *kv.Store, notifyMgr *notify.Manager, circuitMgr *circuit.Manager, groupMgr *agentgroup.Manager, sched *scheduler.Manager, pipelineMgr *pipeline.Manager, meter *billing.Meter) http.Handler {
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

	mux.HandleFunc("/auth/whoami", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodPost:
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		info, ok := requestAuthFromContext(r.Context())
		if !ok {
			info, ok = resolvePresentedCredential(r, auth)
		}
		if !ok && anyAuthConfigured(auth) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !ok {
			info = requestAuth{
				access: "anonymous",
				tenant: auth.defaultTenant,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(authWhoAmIResponse{
			Authenticated: ok,
			Access:        info.access,
			Tenant:        info.tenant,
		})
	})

	mux.HandleFunc("/admin/tenants", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
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

		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			name := strings.TrimSpace(body.Name)
			if name == "" {
				writeError(w, http.StatusBadRequest, "name is required")
				return
			}
			if err := db.EnsureTenant(name); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to create tenant")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"tenant": name, "status": "created"})

		case http.MethodDelete:
			name := strings.TrimSpace(r.URL.Query().Get("name"))
			if name == "" {
				writeError(w, http.StatusBadRequest, "name is required")
				return
			}
			if err := db.DeleteTenant(name); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeError(w, http.StatusNotFound, "tenant not found")
				} else {
					writeError(w, http.StatusBadRequest, err.Error())
				}
				return
			}
			tenantMgr.evict(name)
			w.WriteHeader(http.StatusNoContent)

		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
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

	mux.HandleFunc("/routes/", func(w http.ResponseWriter, r *http.Request) {
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		destination, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/routes/"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid route destination")
			return
		}
		if destination == "" {
			writeError(w, http.StatusBadRequest, "missing route destination")
			return
		}
		switch r.Method {
		case http.MethodGet:
			route, err := state.router.GetRoute(destination)
			if err != nil {
				writeError(w, http.StatusNotFound, "route not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(route)
		case http.MethodPatch:
			var body struct {
				Latency    *int               `json:"latency"`
				Throughput *int               `json:"throughput"`
				TTLSeconds *int               `json:"ttl_seconds"`
				Satellite  *bool              `json:"satellite"`
				Tags       *map[string]string `json:"tags"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			opts := routing.UpdateRouteOptions{
				Satellite: body.Satellite,
				Tags:      body.Tags,
			}
			if body.Latency != nil || body.Throughput != nil {
				current, err := state.router.GetRoute(destination)
				if err != nil {
					writeError(w, http.StatusNotFound, "route not found")
					return
				}
				metric := current.Metric
				if body.Latency != nil {
					metric.Latency = *body.Latency
				}
				if body.Throughput != nil {
					metric.Throughput = *body.Throughput
				}
				opts.Metric = &metric
			}
			if body.TTLSeconds != nil {
				if *body.TTLSeconds < 0 {
					writeError(w, http.StatusBadRequest, "ttl_seconds must be non-negative")
					return
				}
				ttl := time.Duration(*body.TTLSeconds) * time.Second
				opts.TTL = &ttl
			}
			route, err := state.router.UpdateRoute(destination, opts)
			if err != nil {
				if err.Error() == "route not found" {
					writeError(w, http.StatusNotFound, "route not found")
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := db.SaveRoutesTenant(tenant, state.router); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to persist routes")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventRouteUpdated,
					TenantID: tenant,
					Data:     map[string]any{"destination": destination},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(route)
		case http.MethodDelete:
			if err := state.router.DeleteRoute(destination); err != nil {
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
					Data:     map[string]string{"destination": destination},
				})
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	parseRouteFilterQuery := func(r *http.Request) (routing.BestRouteFilter, error) {
		q := r.URL.Query()
		filter := routing.BestRouteFilter{}
		if maxLatStr := strings.TrimSpace(q.Get("max_latency")); maxLatStr != "" {
			maxLat, err := strconv.Atoi(maxLatStr)
			if err != nil {
				return routing.BestRouteFilter{}, fmt.Errorf("max_latency must be an integer")
			}
			filter.MaxLatency = maxLat
		}
		if minThrStr := strings.TrimSpace(q.Get("min_throughput")); minThrStr != "" {
			minThr, err := strconv.Atoi(minThrStr)
			if err != nil {
				return routing.BestRouteFilter{}, fmt.Errorf("min_throughput must be an integer")
			}
			filter.MinThroughput = minThr
		}
		if satStr := strings.TrimSpace(q.Get("satellite")); satStr != "" {
			wantSat, err := strconv.ParseBool(satStr)
			if err != nil {
				return routing.BestRouteFilter{}, fmt.Errorf("satellite must be a boolean")
			}
			filter.Satellite = &wantSat
		}
		tagFilter, err := parseTagsStrict(q["tag"])
		if err != nil {
			return routing.BestRouteFilter{}, err
		}
		if len(tagFilter) > 0 {
			filter.Tags = tagFilter
		}
		return filter, nil
	}

	parseSatellitePenalty := func(r *http.Request) (int, error) {
		q := r.URL.Query()
		satellitePenalty := routing.SatellitePenaltyMs
		if penaltyStr := strings.TrimSpace(q.Get("satellite_penalty")); penaltyStr != "" {
			penalty, err := strconv.Atoi(penaltyStr)
			if err != nil {
				return 0, fmt.Errorf("satellite_penalty must be an integer")
			}
			satellitePenalty = penalty
		}
		return satellitePenalty, nil
	}

	type resolveRequest struct {
		ID               string            `json:"id,omitempty"`
		Region           string            `json:"region,omitempty"`
		Site             string            `json:"site,omitempty"`
		MaxScope         string            `json:"max_scope,omitempty"`
		Explain          bool              `json:"explain,omitempty"`
		Alternatives     int               `json:"alternatives,omitempty"`
		MaxLatency       int               `json:"max_latency,omitempty"`
		MinThroughput    int               `json:"min_throughput,omitempty"`
		Satellite        *bool             `json:"satellite,omitempty"`
		Tags             map[string]string `json:"tags,omitempty"`
		SatellitePenalty *int              `json:"satellite_penalty,omitempty"`
	}
	type resolveRequestInput struct {
		ID               string            `json:"id,omitempty"`
		Region           *string           `json:"region,omitempty"`
		Site             *string           `json:"site,omitempty"`
		MaxScope         *string           `json:"max_scope,omitempty"`
		Explain          *bool             `json:"explain,omitempty"`
		Alternatives     *int              `json:"alternatives,omitempty"`
		MaxLatency       *int              `json:"max_latency,omitempty"`
		MinThroughput    *int              `json:"min_throughput,omitempty"`
		Satellite        *bool             `json:"satellite,omitempty"`
		Tags             map[string]string `json:"tags,omitempty"`
		SatellitePenalty *int              `json:"satellite_penalty,omitempty"`
	}
	type resolveBatchBody struct {
		Defaults *resolveRequestInput  `json:"defaults,omitempty"`
		Requests []resolveRequestInput `json:"requests"`
	}

	scopeRank := map[string]int{"site": 1, "region": 2, "global": 3}
	mergeResolveRequest := func(defaults *resolveRequestInput, item resolveRequestInput) resolveRequest {
		merged := resolveRequest{ID: strings.TrimSpace(item.ID)}
		if defaults != nil {
			if defaults.Region != nil {
				merged.Region = strings.TrimSpace(*defaults.Region)
			}
			if defaults.Site != nil {
				merged.Site = strings.TrimSpace(*defaults.Site)
			}
			if defaults.MaxScope != nil {
				merged.MaxScope = strings.TrimSpace(*defaults.MaxScope)
			}
			if defaults.Explain != nil {
				merged.Explain = *defaults.Explain
			}
			if defaults.Alternatives != nil {
				merged.Alternatives = *defaults.Alternatives
			}
			if defaults.MaxLatency != nil {
				merged.MaxLatency = *defaults.MaxLatency
			}
			if defaults.MinThroughput != nil {
				merged.MinThroughput = *defaults.MinThroughput
			}
			if defaults.Satellite != nil {
				sat := *defaults.Satellite
				merged.Satellite = &sat
			}
			if len(defaults.Tags) > 0 {
				merged.Tags = cloneStringMap(defaults.Tags)
			}
			if defaults.SatellitePenalty != nil {
				penalty := *defaults.SatellitePenalty
				merged.SatellitePenalty = &penalty
			}
		}
		if item.Region != nil {
			merged.Region = strings.TrimSpace(*item.Region)
		}
		if item.Site != nil {
			merged.Site = strings.TrimSpace(*item.Site)
		}
		if item.MaxScope != nil {
			merged.MaxScope = strings.TrimSpace(*item.MaxScope)
		}
		if item.Explain != nil {
			merged.Explain = *item.Explain
		}
		if item.Alternatives != nil {
			merged.Alternatives = *item.Alternatives
		}
		if item.MaxLatency != nil {
			merged.MaxLatency = *item.MaxLatency
		}
		if item.MinThroughput != nil {
			merged.MinThroughput = *item.MinThroughput
		}
		if item.Satellite != nil {
			sat := *item.Satellite
			merged.Satellite = &sat
		}
		if item.Tags != nil {
			merged.Tags = cloneStringMap(item.Tags)
		}
		if item.SatellitePenalty != nil {
			penalty := *item.SatellitePenalty
			merged.SatellitePenalty = &penalty
		}
		return merged
	}
	resolveRoute := func(routes []routing.Route, req resolveRequest) (int, map[string]any) {
		region := strings.TrimSpace(req.Region)
		site := strings.TrimSpace(req.Site)
		maxScope := strings.TrimSpace(req.MaxScope)
		if maxScope == "" {
			maxScope = "global"
		}
		if _, ok := scopeRank[maxScope]; !ok {
			return http.StatusBadRequest, map[string]any{"error": "max_scope must be one of: site, region, global"}
		}
		baseScope := "global"
		if region != "" {
			baseScope = "region"
		}
		if site != "" {
			baseScope = "site"
		}
		if scopeRank[maxScope] < scopeRank[baseScope] {
			return http.StatusBadRequest, map[string]any{"error": "max_scope cannot be narrower than the requested scope"}
		}
		if req.Alternatives < 0 {
			return http.StatusBadRequest, map[string]any{"error": "alternatives must be a non-negative integer"}
		}
		satellitePenalty := routing.SatellitePenaltyMs
		if req.SatellitePenalty != nil {
			satellitePenalty = *req.SatellitePenalty
		}
		filter := routing.BestRouteFilter{
			MaxLatency:    req.MaxLatency,
			MinThroughput: req.MinThroughput,
			Satellite:     req.Satellite,
			Tags:          req.Tags,
		}
		attempts := make([]map[string]any, 0, 3)
		explanation := func() map[string]any {
			return map[string]any{
				"requested": map[string]any{"region": region, "site": site, "max_scope": maxScope},
				"attempts":  attempts,
			}
		}

		selectScope := func(scope string, candidates []routing.Route) (map[string]any, bool) {
			filteredCandidates := routing.RankedRoutesFromRoutesFiltered(candidates, filter, satellitePenalty)
			attempt := map[string]any{
				"scope":               scope,
				"candidate_count":     len(candidates),
				"matching_candidates": len(filteredCandidates),
			}
			attempts = append(attempts, attempt)
			if len(filteredCandidates) == 0 {
				return nil, false
			}
			attempt["selected"] = true
			resp := map[string]any{"scope": scope, "route": filteredCandidates[0]}
			if req.Alternatives > 0 {
				alternatives := []routing.Route{}
				if len(filteredCandidates) > 1 {
					end := 1 + req.Alternatives
					if end > len(filteredCandidates) {
						end = len(filteredCandidates)
					}
					alternatives = filteredCandidates[1:end]
				}
				resp["alternatives"] = alternatives
			}
			if req.Explain {
				resp["explanation"] = explanation()
			}
			return resp, true
		}

		if site != "" && scopeRank[maxScope] >= scopeRank["site"] {
			candidates := make([]routing.Route, 0)
			for _, rt := range routes {
				if strings.TrimSpace(rt.Tags["site"]) != site {
					continue
				}
				if region != "" && strings.TrimSpace(rt.Tags["region"]) != region {
					continue
				}
				candidates = append(candidates, rt)
			}
			if resp, ok := selectScope("site", candidates); ok {
				return http.StatusOK, resp
			}
		}
		if region != "" && scopeRank[maxScope] >= scopeRank["region"] {
			candidates := make([]routing.Route, 0)
			for _, rt := range routes {
				if strings.TrimSpace(rt.Tags["region"]) == region {
					candidates = append(candidates, rt)
				}
			}
			if resp, ok := selectScope("region", candidates); ok {
				return http.StatusOK, resp
			}
		}

		candidates := make([]routing.Route, 0)
		for _, rt := range routes {
			if strings.TrimSpace(rt.Tags["region"]) != "" || strings.TrimSpace(rt.Tags["site"]) != "" {
				continue
			}
			candidates = append(candidates, rt)
		}
		if scopeRank[maxScope] >= scopeRank["global"] {
			if resp, ok := selectScope("global", candidates); ok {
				return http.StatusOK, resp
			}
		}
		resp := map[string]any{"error": "no routes available"}
		if req.Explain {
			resp["explanation"] = explanation()
		}
		return http.StatusNotFound, resp
	}

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
		filter, err := parseRouteFilterQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		satellitePenalty, err := parseSatellitePenalty(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		best, err := state.router.SelectBestNextHopFiltered(filter, satellitePenalty)
		if err != nil {
			writeError(w, http.StatusNotFound, "no routes available")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(best)
	})

	// GET /routes/resolve — resolve the nearest edge route with site/region/global fallback.
	mux.HandleFunc("/routes/resolve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		filter, err := parseRouteFilterQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		satellitePenalty, err := parseSatellitePenalty(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		explain := false
		if explainStr := strings.TrimSpace(r.URL.Query().Get("explain")); explainStr != "" {
			wantExplain, err := strconv.ParseBool(explainStr)
			if err != nil {
				writeError(w, http.StatusBadRequest, "explain must be a boolean")
				return
			}
			explain = wantExplain
		}
		alternativesLimit := 0
		if altStr := strings.TrimSpace(r.URL.Query().Get("alternatives")); altStr != "" {
			parsed, err := strconv.Atoi(altStr)
			if err != nil || parsed < 0 {
				writeError(w, http.StatusBadRequest, "alternatives must be a non-negative integer")
				return
			}
			alternativesLimit = parsed
		}
		req := resolveRequest{
			Region:           strings.TrimSpace(r.URL.Query().Get("region")),
			Site:             strings.TrimSpace(r.URL.Query().Get("site")),
			MaxScope:         strings.TrimSpace(r.URL.Query().Get("max_scope")),
			Explain:          explain,
			Alternatives:     alternativesLimit,
			MaxLatency:       filter.MaxLatency,
			MinThroughput:    filter.MinThroughput,
			Satellite:        filter.Satellite,
			Tags:             filter.Tags,
			SatellitePenalty: &satellitePenalty,
		}
		status, resp := resolveRoute(state.router.ListRoutes(), req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	})

	// POST /routes/resolve/batch — resolve multiple edge route requests in one call.
	mux.HandleFunc("/routes/resolve/batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		var body resolveBatchBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if len(body.Requests) == 0 {
			writeError(w, http.StatusBadRequest, "requests must not be empty")
			return
		}
		routes := state.router.ListRoutes()
		results := make([]map[string]any, 0, len(body.Requests))
		summary := map[string]int{
			"ok":        0,
			"not_found": 0,
			"invalid":   0,
		}
		for i, req := range body.Requests {
			merged := mergeResolveRequest(body.Defaults, req)
			status, result := resolveRoute(routes, merged)
			result["status"] = status
			result["index"] = i
			if merged.ID != "" {
				result["id"] = merged.ID
			}
			switch status {
			case http.StatusOK:
				summary["ok"]++
			case http.StatusNotFound:
				summary["not_found"]++
			default:
				summary["invalid"]++
			}
			results = append(results, result)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":   len(results),
			"summary": summary,
			"results": results,
		})
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

	// GET /blockchain/verify — verifies chain hash linkage for the tenant.
	mux.HandleFunc("/blockchain/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		badIndex, valid := state.chain.VerifyChain()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"valid":     valid,
			"height":    state.chain.Height(),
			"bad_index": badIndex,
		})
	})

	// GET /blockchain/search?sender=X&recipient=Y — find blocks by transaction parties.
	mux.HandleFunc("/blockchain/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		sender := strings.TrimSpace(r.URL.Query().Get("sender"))
		recipient := strings.TrimSpace(r.URL.Query().Get("recipient"))
		if sender == "" && recipient == "" {
			writeError(w, http.StatusBadRequest, "sender or recipient is required")
			return
		}
		blocks := state.chain.SearchTransactions(sender, recipient)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":  len(blocks),
			"blocks": blocks,
		})
	})

	// GET /blockchain/export — export full chain as a downloadable JSON file.
	mux.HandleFunc("/blockchain/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		export := blockchainFile{
			Version:   1,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Blocks:    state.chain.Snapshot(),
			Meta:      map[string]string{"tenant": tenant},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="blockchain-`+tenant+`.json"`)
		_ = json.NewEncoder(w).Encode(export)
	})

	// GET /routes/export — export all routes as a downloadable JSON file.
	mux.HandleFunc("/routes/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		export := routesFile{
			Version:   1,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Routes:    state.router.ListRoutes(),
			Meta:      map[string]string{"tenant": tenant},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="routes-`+tenant+`.json"`)
		_ = json.NewEncoder(w).Encode(export)
	})

	// POST /routes/batch — add multiple routes in a single request.
	// DELETE /routes/batch — delete multiple routes by destination list.
	type batchRouteEntry struct {
		Destination string            `json:"destination"`
		Latency     int               `json:"latency"`
		Throughput  int               `json:"throughput"`
		TTLSeconds  int               `json:"ttl_seconds"`
		Satellite   bool              `json:"satellite"`
		Tags        map[string]string `json:"tags"`
	}
	mux.HandleFunc("/routes/batch", func(w http.ResponseWriter, r *http.Request) {
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
			var entries []batchRouteEntry
			if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if len(entries) == 0 {
				writeError(w, http.StatusBadRequest, "at least one route is required")
				return
			}
			if limits.maxRoutes > 0 && state.router.RouteCount()+len(entries) > limits.maxRoutes {
				writeError(w, http.StatusTooManyRequests, "tenant route limit would be exceeded")
				return
			}
			var added int
			for _, e := range entries {
				var ttl time.Duration
				if e.TTLSeconds > 0 {
					ttl = time.Duration(e.TTLSeconds) * time.Second
				}
				if err := state.router.AddRouteWithOptions(e.Destination, routing.RouteMetric{
					Latency:    e.Latency,
					Throughput: e.Throughput,
				}, routing.RouteOptions{
					TTL:       ttl,
					Satellite: e.Satellite,
					Tags:      e.Tags,
				}); err == nil {
					added++
				}
			}
			if err := db.SaveRoutesTenant(tenant, state.router); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to persist routes")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]int{"added": added, "submitted": len(entries)})

		case http.MethodDelete:
			r.Body = http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))
			var destinations []string
			if err := json.NewDecoder(r.Body).Decode(&destinations); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if len(destinations) == 0 {
				writeError(w, http.StatusBadRequest, "at least one destination is required")
				return
			}
			deleted, notFound := 0, 0
			for _, dst := range destinations {
				dst = strings.TrimSpace(dst)
				if dst == "" {
					continue
				}
				if err := state.router.DeleteRoute(dst); err != nil {
					notFound++
				} else {
					deleted++
				}
			}
			if deleted > 0 {
				if err := db.SaveRoutesTenant(tenant, state.router); err != nil {
					writeError(w, http.StatusInternalServerError, "failed to persist routes")
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]int{
				"deleted":   deleted,
				"not_found": notFound,
			})

		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET /mesh/peers — per-peer health data.
	mux.HandleFunc("/mesh/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if meshNode == nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "disabled"})
			return
		}
		_ = json.NewEncoder(w).Encode(meshNode.PeerHealth())
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
			// Single-agent lookup when ?id= is provided.
			if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
				a, ok := agentReg.Get(tenant, id)
				if !ok {
					writeError(w, http.StatusNotFound, "agent not found")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(a)
				return
			}
			capability := strings.TrimSpace(r.URL.Query().Get("capability"))
			selectorStr := strings.TrimSpace(r.URL.Query().Get("selector"))
			var agents []agent.Agent
			switch {
			case capability != "":
				agents = agentReg.ListByCapability(tenant, capability)
			case selectorStr != "":
				sel := parseTagSelector(selectorStr)
				agents = agentReg.ListBySelector(tenant, sel)
			default:
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
		case http.MethodPatch:
			// PATCH /agents?id=X — update capabilities, tags, addr, or status.
			id := strings.TrimSpace(r.URL.Query().Get("id"))
			if id == "" {
				writeError(w, http.StatusBadRequest, "id is required")
				return
			}
			var req agent.UpdateRequest
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if req.Status != "" && !agent.IsValidStatus(req.Status) {
				writeError(w, http.StatusBadRequest, "invalid status: must be healthy, degraded, or unknown")
				return
			}
			if err := agentReg.Update(tenant, id, req); err != nil {
				writeError(w, http.StatusNotFound, "agent not found")
				return
			}
			a, _ := agentReg.Get(tenant, id)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a)
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

	// GET /events/history?limit=N — returns recent events from the ring buffer.
	mux.HandleFunc("/events/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if eventBroker == nil {
			writeError(w, http.StatusServiceUnavailable, "event broker unavailable")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		limit, _, _ := parseIntQueryMulti(r, "limit")
		typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
		all := eventBroker.History(limit)
		// Filter by tenant and optional event type.
		out := make([]events.Event, 0, len(all))
		for _, ev := range all {
			if strings.TrimSpace(tenant) != "" && ev.TenantID != "" && ev.TenantID != tenant {
				continue
			}
			if typeFilter != "" && string(ev.Type) != typeFilter {
				continue
			}
			out = append(out, ev)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count":  len(out),
			"events": out,
		})
	})

	// POST /admin/backup — trigger an on-demand backup immediately.
	mux.HandleFunc("/admin/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if status == nil || strings.TrimSpace(status.dbPath) == "" {
			writeError(w, http.StatusServiceUnavailable, "backup not configured")
			return
		}
		if strings.TrimSpace(status.backupDir) == "" {
			writeError(w, http.StatusServiceUnavailable, "backup directory not configured")
			return
		}
		if err := os.MkdirAll(status.backupDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create backup directory")
			return
		}
		if err := runBackup(status.dbPath, status.backupDir, status.backupRetention, status); err != nil {
			writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "backup completed",
		})
	})

	// POST /admin/compact — trigger an on-demand compaction immediately.
	mux.HandleFunc("/admin/compact", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if status == nil || strings.TrimSpace(status.dbPath) == "" {
			writeError(w, http.StatusServiceUnavailable, "compact not configured")
			return
		}
		if strings.TrimSpace(status.compactionDir) == "" {
			writeError(w, http.StatusServiceUnavailable, "compaction directory not configured")
			return
		}
		if err := os.MkdirAll(status.compactionDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create compaction directory")
			return
		}
		if err := runCompaction(status.dbPath, status.compactionDir, status.compactionRetention, status); err != nil {
			writeError(w, http.StatusInternalServerError, "compact failed: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "compaction completed",
		})
	})

	// GET /admin/backup/list — list backup files in the backup directory.
	mux.HandleFunc("/admin/backup/list", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if status == nil || strings.TrimSpace(status.backupDir) == "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"backups": []any{}})
			return
		}
		pattern := filepath.Join(status.backupDir, "*.bak")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list backups")
			return
		}
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		type backupEntry struct {
			Name    string `json:"name"`
			SizeB   int64  `json:"size_bytes"`
			ModTime string `json:"mod_time"`
		}
		entries := make([]backupEntry, 0, len(matches))
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			entries = append(entries, backupEntry{
				Name:    filepath.Base(path),
				SizeB:   info.Size(),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"backup_dir": status.backupDir,
			"count":      len(entries),
			"backups":    entries,
		})
	})

	// POST /agents/status?id=X&status=Y — update the status of a registered agent.
	mux.HandleFunc("/agents/status", func(w http.ResponseWriter, r *http.Request) {
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
		statusStr := strings.TrimSpace(r.URL.Query().Get("status"))
		if id == "" {
			writeError(w, http.StatusBadRequest, "id is required")
			return
		}
		if statusStr == "" {
			writeError(w, http.StatusBadRequest, "status is required")
			return
		}
		s := agent.Status(statusStr)
		if !agent.IsValidStatus(s) {
			writeError(w, http.StatusBadRequest, "invalid status: must be healthy, degraded, or unknown")
			return
		}
		if err := agentReg.UpdateStatus(tenant, id, s); err != nil {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /agents/route?capability=X&count=N — return agents that have a capability, best-available first.
	mux.HandleFunc("/agents/route", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		capability := strings.TrimSpace(r.URL.Query().Get("capability"))
		if capability == "" {
			writeError(w, http.StatusBadRequest, "capability is required")
			return
		}
		count := 5
		if c, err := strconv.Atoi(r.URL.Query().Get("count")); err == nil && c > 0 {
			count = c
		}
		candidates := agentReg.ListByCapability(tenant, capability)
		if len(candidates) > count {
			candidates = candidates[:count]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capability": capability,
			"count":      len(candidates),
			"agents":     candidates,
		})
	})

	// GET /agents/jobs — list jobs for the tenant.
	// POST /agents/jobs — submit a new job.
	mux.HandleFunc("/agents/jobs", func(w http.ResponseWriter, r *http.Request) {
		if jobQueue == nil {
			writeError(w, http.StatusServiceUnavailable, "job queue unavailable")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			list := jobQueue.List(tenant)
			if list == nil {
				list = []jobs.Job{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(list), "jobs": list})
		case http.MethodPost:
			var req struct {
				AgentID    string         `json:"agent_id"`
				Capability string         `json:"capability"`
				Payload    map[string]any `json:"payload"`
				Priority   int            `json:"priority"`
				TTLSeconds int            `json:"ttl_seconds"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.AgentID == "" && req.Capability == "" {
				writeError(w, http.StatusBadRequest, "agent_id or capability is required")
				return
			}
			// Auto-dispatch: if only a capability was given, assign to the best
			// available healthy agent that advertises that capability.
			if req.AgentID == "" && req.Capability != "" {
				best, ok := agentReg.FindBest(tenant, req.Capability)
				if !ok {
					writeError(w, http.StatusServiceUnavailable, "no healthy agent available for capability: "+req.Capability)
					return
				}
				req.AgentID = best.ID
			}
			j := jobQueue.SubmitWithOpts(tenant, req.AgentID, req.Capability, req.Payload, jobs.SubmitOptions{
				Priority:   req.Priority,
				TTLSeconds: req.TTLSeconds,
			})
			meter.Record(tenant, billing.MetricJobs, 1)
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventJobCreated,
					TenantID: tenant,
					Data:     j,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(j)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// /agents/jobs/claim   — GET: agent claims its next pending job
	// /agents/jobs/{id}   — PATCH: update status   DELETE: cancel
	mux.HandleFunc("/agents/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if jobQueue == nil {
			writeError(w, http.StatusServiceUnavailable, "job queue unavailable")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/agents/jobs/")

		// GET /agents/jobs/claim?agent_id=X&capability=Y
		if path == "claim" {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
			capability := strings.TrimSpace(r.URL.Query().Get("capability"))
			if agentID == "" && capability == "" {
				writeError(w, http.StatusBadRequest, "agent_id or capability is required")
				return
			}
			j, ok := jobQueue.ClaimForTenant(tenant, agentID, capability)
			if !ok {
				writeError(w, http.StatusNoContent, "no pending job available")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventJobClaimed,
					TenantID: tenant,
					Data:     j,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(j)
			return
		}

		id := path
		if id == "" {
			writeError(w, http.StatusBadRequest, "job id is required")
			return
		}

		switch r.Method {
		case http.MethodGet:
			j, ok := jobQueue.Get(id)
			if !ok || j.Tenant != tenant {
				writeError(w, http.StatusNotFound, "job not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(j)

		case http.MethodPatch:
			existing, ok := jobQueue.Get(id)
			if !ok || existing.Tenant != tenant {
				writeError(w, http.StatusNotFound, "job not found")
				return
			}
			var req struct {
				Status jobs.Status `json:"status"`
				Result string      `json:"result,omitempty"`
				Error  string      `json:"error,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.Status == "" {
				writeError(w, http.StatusBadRequest, "status is required")
				return
			}
			if !jobQueue.Update(id, req.Status, req.Result, req.Error) {
				writeError(w, http.StatusNotFound, "job not found")
				return
			}
			j, _ := jobQueue.Get(id)
			if eventBroker != nil {
				evType := events.EventJobUpdated
				switch req.Status {
				case jobs.StatusDone:
					evType = events.EventJobCompleted
				case jobs.StatusFailed:
					evType = events.EventJobFailed
				case jobs.StatusCancelled:
					evType = events.EventJobCancelled
				case jobs.StatusRunning:
					evType = events.EventJobClaimed
				}
				eventBroker.Publish(events.Event{
					Type:     evType,
					TenantID: tenant,
					Data: map[string]any{
						"previous_status": existing.Status,
						"job":             j,
					},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(j)

		case http.MethodDelete:
			existing, ok := jobQueue.Get(id)
			if !ok || existing.Tenant != tenant {
				writeError(w, http.StatusNotFound, "job not found or already in terminal state")
				return
			}
			if !jobQueue.Cancel(id) {
				writeError(w, http.StatusNotFound, "job not found or already in terminal state")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventJobCancelled,
					TenantID: tenant,
					Data:     existing,
				})
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET /audit?tenant=X&limit=N — query the audit log.
	mux.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		// Admin can override tenant filter.
		if t := strings.TrimSpace(r.URL.Query().Get("tenant")); t != "" {
			tenant = t
		}
		limit := 50
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
			limit = l
		}
		entries, err := db.AuditList(tenant, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read audit log")
			return
		}
		if entries == nil {
			entries = []store.AuditEntry{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": len(entries), "entries": entries})
	})

	// GET /metrics/json — JSON snapshot of key operational metrics.
	mux.HandleFunc("/metrics/json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		names, _ := db.TenantNames()
		if auth.defaultTenant != "" && !stringInSlice(auth.defaultTenant, names) {
			names = append(names, auth.defaultTenant)
		}
		type tenantMetric struct {
			Tenant string `json:"tenant"`
			Blocks int    `json:"blocks"`
			Routes int    `json:"routes"`
			Agents int    `json:"agents"`
		}
		tenantMetrics := make([]tenantMetric, 0, len(names))
		for _, name := range names {
			state, err := tenantMgr.getTenant(name)
			if err != nil {
				continue
			}
			tenantMetrics = append(tenantMetrics, tenantMetric{
				Tenant: name,
				Blocks: state.chain.Height(),
				Routes: state.router.RouteCount(),
				Agents: agentReg.CountByTenant(name),
			})
		}
		jobCount := 0
		if jobQueue != nil {
			jobCount = jobQueue.Count()
		}
		snap := map[string]any{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"tenants":   tenantMetrics,
			"jobs":      jobCount,
		}
		if status != nil {
			now := time.Now().UTC()
			snap["uptime_seconds"] = int64(now.Sub(status.startedAt).Seconds())
		}
		if meshNode != nil {
			stats, _ := meshNode.Snapshot()
			snap["mesh_peers"] = stats
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	})

	// GET /metrics/timeseries?n=60 — last N sampled metrics points.
	mux.HandleFunc("/metrics/timeseries", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		n := 60
		if ns := r.URL.Query().Get("n"); ns != "" {
			if parsed, err := strconv.Atoi(ns); err == nil && parsed > 0 {
				n = parsed
			}
		}
		points := globalMetricsBuf.last(n)
		if points == nil {
			points = []metricsPoint{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(points)
	})

	// GET /routes/stats — aggregate stats for all routes.
	mux.HandleFunc("/routes/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		filter, err := parseRouteFilterQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		routes := routing.FilterRoutes(state.router.ListRoutes(), filter)
		total := len(routes)
		online := 0
		satellite := 0
		var minLat, maxLat int
		var sumLat float64
		dests := make([]string, 0, total)
		type routeStatsBucket struct {
			Count        int
			Satellite    int
			MinLatency   int
			MaxLatency   int
			SumLatency   float64
			Destinations []string
			First        bool
		}
		updateBucket := func(bucket map[string]*routeStatsBucket, key string, rt routing.Route) {
			if key == "" {
				return
			}
			entry := bucket[key]
			if entry == nil {
				entry = &routeStatsBucket{First: true}
				bucket[key] = entry
			}
			entry.Count++
			if rt.Satellite {
				entry.Satellite++
			}
			lat := rt.Metric.Latency
			if entry.First || lat < entry.MinLatency {
				entry.MinLatency = lat
			}
			if entry.First || lat > entry.MaxLatency {
				entry.MaxLatency = lat
			}
			entry.SumLatency += float64(lat)
			entry.Destinations = append(entry.Destinations, rt.Destination)
			entry.First = false
		}
		flattenBucket := func(bucket map[string]*routeStatsBucket) map[string]any {
			out := make(map[string]any, len(bucket))
			for key, entry := range bucket {
				avgLatency := 0.0
				if entry.Count > 0 {
					avgLatency = entry.SumLatency / float64(entry.Count)
				}
				out[key] = map[string]any{
					"count":          entry.Count,
					"satellite":      entry.Satellite,
					"avg_latency_ms": avgLatency,
					"min_latency_ms": entry.MinLatency,
					"max_latency_ms": entry.MaxLatency,
					"destinations":   entry.Destinations,
				}
			}
			return out
		}
		byRegion := map[string]*routeStatsBucket{}
		bySite := map[string]*routeStatsBucket{}
		first := true
		for _, rt := range routes {
			online++
			if rt.Satellite {
				satellite++
			}
			lat := rt.Metric.Latency
			if first || lat < minLat {
				minLat = lat
			}
			if lat > maxLat {
				maxLat = lat
			}
			sumLat += float64(lat)
			dests = append(dests, rt.Destination)
			updateBucket(byRegion, rt.Tags["region"], rt)
			updateBucket(bySite, rt.Tags["site"], rt)
			first = false
		}
		avgLat := 0.0
		if total > 0 {
			avgLat = sumLat / float64(total)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":          total,
			"online":         online,
			"satellite":      satellite,
			"avg_latency_ms": avgLat,
			"min_latency_ms": minLat,
			"max_latency_ms": maxLat,
			"destinations":   dests,
			"by_region":      flattenBucket(byRegion),
			"by_site":        flattenBucket(bySite),
		})
	})

	// GET /routes/topology — edge-oriented topology grouped by region and site.
	mux.HandleFunc("/routes/topology", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		filter, err := parseRouteFilterQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		satellitePenalty, err := parseSatellitePenalty(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		routes := routing.FilterRoutes(state.router.ListRoutes(), filter)
		regions := map[string][]routing.Route{}
		sitesByRegion := map[string]map[string][]routing.Route{}
		untagged := make([]routing.Route, 0)
		for _, rt := range routes {
			region := strings.TrimSpace(rt.Tags["region"])
			site := strings.TrimSpace(rt.Tags["site"])
			if region == "" {
				untagged = append(untagged, rt)
				continue
			}
			regions[region] = append(regions[region], rt)
			if site != "" {
				if sitesByRegion[region] == nil {
					sitesByRegion[region] = map[string][]routing.Route{}
				}
				sitesByRegion[region][site] = append(sitesByRegion[region][site], rt)
			}
		}

		regionPayload := map[string]any{}
		for region, regionRoutes := range regions {
			regionBest, _ := routing.BestRouteFromRoutes(regionRoutes, satellitePenalty)
			sitePayload := map[string]any{}
			for site, siteRoutes := range sitesByRegion[region] {
				siteBest, _ := routing.BestRouteFromRoutes(siteRoutes, satellitePenalty)
				sitePayload[site] = map[string]any{
					"count":      len(siteRoutes),
					"best_route": siteBest,
				}
			}
			regionPayload[region] = map[string]any{
				"count":      len(regionRoutes),
				"best_route": regionBest,
				"sites":      sitePayload,
			}
		}

		resp := map[string]any{
			"total":   len(routes),
			"regions": regionPayload,
		}
		if len(untagged) > 0 {
			best, _ := routing.BestRouteFromRoutes(untagged, satellitePenalty)
			resp["untagged"] = map[string]any{
				"count":      len(untagged),
				"best_route": best,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// GET /agents/stats — aggregate stats for all agents.
	mux.HandleFunc("/agents/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		agents := agentReg.List(tenant)
		byCap := map[string]int{}
		byStatus := map[string]int{}
		byTenant := map[string]int{}
		for _, a := range agents {
			for _, c := range a.Capabilities {
				byCap[c]++
			}
			byStatus[string(a.Status)]++
			byTenant[a.TenantID]++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":         len(agents),
			"by_capability": byCap,
			"by_status":     byStatus,
			"by_tenant":     byTenant,
		})
	})

	// GET /blockchain/stats — aggregate blockchain statistics.
	mux.HandleFunc("/blockchain/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		blocks := state.chain.Snapshot()
		height := len(blocks)
		totalTxns := 0
		senders := map[string]bool{}
		recipients := map[string]bool{}
		genesisTime := ""
		latestTime := ""
		for _, b := range blocks {
			totalTxns += len(b.Transactions)
			for _, tx := range b.Transactions {
				if tx.Sender != "" {
					senders[tx.Sender] = true
				}
				if tx.Recipient != "" {
					recipients[tx.Recipient] = true
				}
			}
			if genesisTime == "" && b.Timestamp != "" {
				genesisTime = b.Timestamp
			}
			if b.Timestamp != "" {
				latestTime = b.Timestamp
			}
		}
		avgTxPerBlock := 0.0
		if height > 0 {
			avgTxPerBlock = float64(totalTxns) / float64(height)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"height":             height,
			"total_transactions": totalTxns,
			"unique_senders":     len(senders),
			"unique_recipients":  len(recipients),
			"avg_tx_per_block":   avgTxPerBlock,
			"genesis_time":       genesisTime,
			"latest_time":        latestTime,
		})
	})

	// GET /system/info — runtime and process info.
	mux.HandleFunc("/system/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		uptimeSecs := int64(0)
		dataDirVal := ""
		if status != nil {
			uptimeSecs = int64(time.Since(status.startedAt).Seconds())
			dataDirVal = status.dataDir
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":        "1.0.0",
			"go_version":     runtime.Version(),
			"os":             runtime.GOOS,
			"arch":           runtime.GOARCH,
			"pid":            os.Getpid(),
			"goroutines":     runtime.NumGoroutine(),
			"heap_mb":        float64(ms.HeapAlloc) / (1 << 20),
			"num_cpu":        runtime.NumCPU(),
			"data_dir":       dataDirVal,
			"uptime_seconds": uptimeSecs,
		})
	})

	// GET /agents/health — per-agent health check results from the background checker.
	mux.HandleFunc("/agents/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if hcChecker == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "disabled", "results": []any{}})
			return
		}
		results := hcChecker.Results()
		if results == nil {
			results = []healthcheck.Result{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": len(results), "results": results})
	})

	// POST /blockchain/import — merge blocks from an exported blockchainFile.
	mux.HandleFunc("/blockchain/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		var imp blockchainFile
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&imp); err != nil {
			writeError(w, http.StatusBadRequest, "invalid import file")
			return
		}
		if len(imp.Blocks) == 0 {
			writeError(w, http.StatusBadRequest, "no blocks in import")
			return
		}
		// Validate the imported chain is self-consistent.
		impChain := blockchain.NewBlockchainFromBlocks(nil)
		impChain.Load(imp.Blocks)
		if _, valid := impChain.VerifyChain(); !valid {
			writeError(w, http.StatusBadRequest, "import chain is invalid")
			return
		}
		currentHeight := state.chain.Height()
		added := 0
		for _, b := range imp.Blocks {
			if b.Index < currentHeight {
				continue // already have this block
			}
			state.chain.AddBlock(b.Transactions)
			added++
		}
		if added > 0 {
			_ = db.SaveChainTenant(tenant, state.chain)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"imported": added,
			"skipped":  len(imp.Blocks) - added,
			"height":   state.chain.Height(),
		})
	})

	// POST /routes/import — add routes from an exported routesFile.
	mux.HandleFunc("/routes/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		var imp routesFile
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&imp); err != nil {
			writeError(w, http.StatusBadRequest, "invalid import file")
			return
		}
		added, skipped := 0, 0
		for _, route := range imp.Routes {
			opts := routing.RouteOptions{
				Satellite: route.Satellite,
				Tags:      route.Tags,
			}
			if route.ExpiresAt != nil {
				ttl := time.Until(*route.ExpiresAt)
				if ttl > 0 {
					opts.TTL = ttl
				}
			}
			if err := state.router.AddRouteWithOptions(route.Destination, route.Metric, opts); err != nil {
				skipped++
				continue
			}
			added++
		}
		if added > 0 {
			_ = db.SaveRoutesTenant(tenant, state.router)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"imported": added,
			"skipped":  skipped,
			"total":    state.router.RouteCount(),
		})
	})

	// KV store endpoints — /kv?prefix=X, /kv/{key}
	mux.HandleFunc("/kv", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			prefix := r.URL.Query().Get("prefix")
			entries := kvStore.List(tenant, prefix)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries, "count": len(entries)})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/kv/", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/kv/")
		if key == "" {
			writeError(w, http.StatusBadRequest, "missing key")
			return
		}
		switch r.Method {
		case http.MethodGet:
			entry, ok := kvStore.Get(tenant, key)
			if !ok {
				writeError(w, http.StatusNotFound, "key not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entry)
		case http.MethodPut:
			var body struct {
				Value string        `json:"value"`
				TTL   time.Duration `json:"ttl"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			kvStore.Set(tenant, key, body.Value, body.TTL)
			if eventBroker != nil {
				eventBroker.Publish(events.Event{Type: "kv.set", TenantID: tenant, Timestamp: time.Now().UTC(), Data: map[string]any{"key": key}})
			}
			entry, _ := kvStore.Get(tenant, key)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entry)
		case http.MethodDelete:
			if !kvStore.Delete(tenant, key) {
				writeError(w, http.StatusNotFound, "key not found")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{Type: "kv.delete", TenantID: tenant, Timestamp: time.Now().UTC(), Data: map[string]any{"key": key}})
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET /search?q=X — unified search across agents, routes, blockchain txns.
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		if q == "" {
			writeError(w, http.StatusBadRequest, "missing q parameter")
			return
		}

		type agentResult struct {
			ID           string   `json:"id"`
			Tenant       string   `json:"tenant"`
			Capabilities []string `json:"capabilities"`
			Status       string   `json:"status"`
		}
		type routeResult struct {
			Destination string `json:"destination"`
			Latency     int    `json:"latency_ms"`
		}
		type txResult struct {
			BlockIndex int     `json:"block_index"`
			Sender     string  `json:"sender"`
			Recipient  string  `json:"recipient"`
			Amount     float64 `json:"amount"`
		}

		var agents []agentResult
		for _, ag := range agentReg.List(tenant) {
			if strings.Contains(strings.ToLower(ag.ID), q) || strings.Contains(strings.ToLower(string(ag.Status)), q) {
				agents = append(agents, agentResult{ID: ag.ID, Tenant: ag.TenantID, Capabilities: ag.Capabilities, Status: string(ag.Status)})
				continue
			}
			for _, cap := range ag.Capabilities {
				if strings.Contains(strings.ToLower(cap), q) {
					agents = append(agents, agentResult{ID: ag.ID, Tenant: ag.TenantID, Capabilities: ag.Capabilities, Status: string(ag.Status)})
					break
				}
			}
		}

		var routes []routeResult
		for _, rt := range state.router.ListRoutes() {
			if strings.Contains(strings.ToLower(rt.Destination), q) {
				routes = append(routes, routeResult{Destination: rt.Destination, Latency: rt.Metric.Latency})
			}
		}

		var txns []txResult
		for _, block := range state.chain.Blocks() {
			for _, tx := range block.Transactions {
				if strings.Contains(strings.ToLower(tx.Sender), q) ||
					strings.Contains(strings.ToLower(tx.Recipient), q) {
					txns = append(txns, txResult{BlockIndex: block.Index, Sender: tx.Sender, Recipient: tx.Recipient, Amount: tx.Amount})
				}
			}
		}

		if agents == nil {
			agents = []agentResult{}
		}
		if routes == nil {
			routes = []routeResult{}
		}
		if txns == nil {
			txns = []txResult{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":        q,
			"agents":       agents,
			"routes":       routes,
			"transactions": txns,
		})
	})

	// GET /routes/filter — advanced route filtering by latency, throughput, satellite flag, and tags.
	mux.HandleFunc("/routes/filter", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		state, _, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		filter, err := parseRouteFilterQuery(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filtered := routing.FilterRoutes(state.router.ListRoutes(), filter)
		if filtered == nil {
			filtered = []routing.Route{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"routes": filtered, "count": len(filtered)})
	})

	// Notification rule endpoints — /admin/notifications
	mux.HandleFunc("/admin/notifications", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			rules := notifyMgr.List(tenant)
			if rules == nil {
				rules = []notify.Rule{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"rules": rules, "count": len(rules)})
		case http.MethodPost:
			var body struct {
				Name       string   `json:"name"`
				WebhookURL string   `json:"webhook_url"`
				Secret     string   `json:"secret"`
				EventTypes []string `json:"event_types"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			if strings.TrimSpace(body.Name) == "" {
				writeError(w, http.StatusBadRequest, "name is required")
				return
			}
			if strings.TrimSpace(body.WebhookURL) == "" {
				writeError(w, http.StatusBadRequest, "webhook_url is required")
				return
			}
			rule := notifyMgr.Add(tenant, body.Name, body.WebhookURL, body.Secret, body.EventTypes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(rule)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/admin/notifications/", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/notifications/")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing rule id")
			return
		}
		switch r.Method {
		case http.MethodGet:
			rule, ok := notifyMgr.Get(tenant, id)
			if !ok {
				writeError(w, http.StatusNotFound, "rule not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rule)
		case http.MethodPatch:
			var body struct {
				Name       *string   `json:"name"`
				WebhookURL *string   `json:"webhook_url"`
				Secret     *string   `json:"secret"`
				EventTypes *[]string `json:"event_types"`
				Active     *bool     `json:"active"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			rule, ok, err := notifyMgr.Update(tenant, id, notify.UpdateParams{
				Name:       body.Name,
				WebhookURL: body.WebhookURL,
				Secret:     body.Secret,
				EventTypes: body.EventTypes,
				Active:     body.Active,
			})
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !ok {
				writeError(w, http.StatusNotFound, "rule not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rule)
		case http.MethodDelete:
			if !notifyMgr.Delete(tenant, id) {
				writeError(w, http.StatusNotFound, "rule not found")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// ── Circuit breaker endpoints ────────────────────────────────────────────

	// GET /circuit — list all breakers; GET /circuit?agent_id=X — single breaker
	mux.HandleFunc("/circuit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if id := r.URL.Query().Get("agent_id"); id != "" {
			b := circuitMgr.Get(id)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(b)
			return
		}
		breakers := circuitMgr.List()
		if breakers == nil {
			breakers = []circuit.Breaker{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"breakers": breakers, "count": len(breakers)})
	})

	// POST /circuit/record — record success or failure
	mux.HandleFunc("/circuit/record", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			AgentID string `json:"agent_id"`
			Success bool   `json:"success"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		if body.AgentID == "" {
			writeError(w, http.StatusBadRequest, "agent_id required")
			return
		}
		if body.Success {
			circuitMgr.RecordSuccess(body.AgentID)
		} else {
			circuitMgr.RecordFailure(body.AgentID)
		}
		b := circuitMgr.Get(body.AgentID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b)
	})

	// POST /circuit/reset?agent_id=X — manually reset a breaker
	mux.HandleFunc("/circuit/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		if id == "" {
			writeError(w, http.StatusBadRequest, "agent_id required")
			return
		}
		circuitMgr.Reset(id)
		b := circuitMgr.Get(id)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b)
	})

	// ── Agent group endpoints ─────────────────────────────────────────────────

	// GET/POST /agent-groups
	mux.HandleFunc("/agent-groups", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			groups := groupMgr.List(tenant)
			if groups == nil {
				groups = []agentgroup.Group{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"groups": groups, "count": len(groups)})
		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			g, err := groupMgr.Create(tenant, body.Name)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventGroupCreated,
					TenantID: tenant,
					Data:     g,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(g)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET/PATCH/DELETE /agent-groups/{id}
	// POST /agent-groups/{id}/members   PATCH/DELETE /agent-groups/{id}/members/{agentID}
	// GET /agent-groups/{id}/next — weighted round-robin next member
	mux.HandleFunc("/agent-groups/", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/agent-groups/")
		parts := strings.SplitN(path, "/", 3)
		groupID := parts[0]
		if groupID == "" {
			writeError(w, http.StatusBadRequest, "missing group id")
			return
		}
		// /agent-groups/{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				g, ok := groupMgr.Get(tenant, groupID)
				if !ok {
					writeError(w, http.StatusNotFound, "group not found")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(g)
			case http.MethodPatch:
				var body struct {
					Name *string `json:"name"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
					writeError(w, http.StatusBadRequest, "invalid body")
					return
				}
				g, ok, err := groupMgr.Update(tenant, groupID, agentgroup.UpdateParams{Name: body.Name})
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if !ok {
					writeError(w, http.StatusNotFound, "group not found")
					return
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventGroupUpdated,
						TenantID: tenant,
						Data:     g,
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(g)
			case http.MethodDelete:
				g, ok := groupMgr.Get(tenant, groupID)
				if !ok {
					writeError(w, http.StatusNotFound, "group not found")
					return
				}
				if !groupMgr.Delete(tenant, groupID) {
					writeError(w, http.StatusNotFound, "group not found")
					return
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventGroupDeleted,
						TenantID: tenant,
						Data:     g,
					})
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}
		sub := parts[1]
		// /agent-groups/{id}/members
		if sub == "members" && len(parts) == 2 {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			var body struct {
				AgentID string `json:"agent_id"`
				Weight  *int   `json:"weight"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			weight := 1
			if body.Weight != nil {
				weight = *body.Weight
			}
			before, ok := groupMgr.Get(tenant, groupID)
			if !ok {
				writeError(w, http.StatusNotFound, "group not found")
				return
			}
			if err := groupMgr.AddMemberWithWeight(tenant, groupID, body.AgentID, weight); err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeError(w, http.StatusNotFound, err.Error())
					return
				}
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			g, _ := groupMgr.Get(tenant, groupID)
			if eventBroker != nil && len(g.Members) > len(before.Members) {
				eventBroker.Publish(events.Event{
					Type:     events.EventGroupMemberAdded,
					TenantID: tenant,
					Data: map[string]any{
						"group_id":   groupID,
						"agent_id":   body.AgentID,
						"weight":     weight,
						"members":    g.Members,
						"group_name": g.Name,
					},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(g)
			return
		}
		// /agent-groups/{id}/members/{agentID}
		if sub == "members" && len(parts) == 3 {
			agentID := parts[2]
			switch r.Method {
			case http.MethodPatch:
				var body struct {
					Weight int `json:"weight"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
					writeError(w, http.StatusBadRequest, "invalid body")
					return
				}
				g, ok, err := groupMgr.UpdateMemberWeight(tenant, groupID, agentID, body.Weight)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if !ok {
					writeError(w, http.StatusNotFound, "group member not found")
					return
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventGroupMemberUpdated,
						TenantID: tenant,
						Data: map[string]any{
							"group_id": groupID,
							"agent_id": agentID,
							"weight":   body.Weight,
						},
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(g)
				return
			case http.MethodDelete:
				if err := groupMgr.RemoveMember(tenant, groupID, agentID); err != nil {
					writeError(w, http.StatusNotFound, err.Error())
					return
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventGroupMemberRemoved,
						TenantID: tenant,
						Data: map[string]any{
							"group_id": groupID,
							"agent_id": agentID,
						},
					})
				}
				w.WriteHeader(http.StatusNoContent)
				return
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
		}
		// /agent-groups/{id}/next
		if sub == "next" && r.Method == http.MethodGet {
			agentID, ok := groupMgr.Next(tenant, groupID, func(id string) bool {
				return circuitMgr.Allow(id)
			})
			if !ok {
				writeError(w, http.StatusServiceUnavailable, "no available members")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"agent_id": agentID, "group_id": groupID})
			return
		}
		writeError(w, http.StatusNotFound, "unknown sub-path")
	})

	// ── Scheduler endpoints ───────────────────────────────────────────────────


	// ── Pipeline endpoints ────────────────────────────────────────────────────

	// GET  /agent-pipelines   — list pipelines
	// POST /agent-pipelines   — create pipeline
	mux.HandleFunc("/agent-pipelines", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			pipelines := pipelineMgr.ListPipelines(tenant)
			if pipelines == nil {
				pipelines = []pipeline.Pipeline{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"pipelines": pipelines, "count": len(pipelines)})
		case http.MethodPost:
			var body struct {
				Name   string           `json:"name"`
				Stages []pipeline.Stage `json:"stages"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			p, err := pipelineMgr.CreatePipeline(tenant, body.Name, body.Stages)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventPipelineCreated,
					TenantID: tenant,
					Data:     p,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(p)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET    /agent-pipelines/{id}                      — get pipeline
	// PATCH  /agent-pipelines/{id}                      — update pipeline
	// DELETE /agent-pipelines/{id}                      — delete pipeline
	// GET    /agent-pipelines/{id}/runs                 — list runs
	// POST   /agent-pipelines/{id}/runs                 — start a run
	// GET    /agent-pipelines/{id}/runs/{runID}         — get run
	// POST   /agent-pipelines/{id}/runs/{runID}/advance — advance to next stage
	mux.HandleFunc("/agent-pipelines/", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/agent-pipelines/")
		parts := strings.SplitN(path, "/", 4)
		pipelineID := parts[0]
		if pipelineID == "" {
			writeError(w, http.StatusBadRequest, "missing pipeline id")
			return
		}

		// /agent-pipelines/{id}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				p, ok := pipelineMgr.GetPipeline(tenant, pipelineID)
				if !ok {
					writeError(w, http.StatusNotFound, "pipeline not found")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(p)
			case http.MethodPatch:
				var body struct {
					Name   *string          `json:"name"`
					Stages []pipeline.Stage `json:"stages"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
					writeError(w, http.StatusBadRequest, "invalid body")
					return
				}
				p, ok, err := pipelineMgr.UpdatePipeline(tenant, pipelineID, pipeline.UpdateParams{
					Name:   body.Name,
					Stages: body.Stages,
				})
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if !ok {
					writeError(w, http.StatusNotFound, "pipeline not found")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(p)
			case http.MethodDelete:
				if !pipelineMgr.DeletePipeline(tenant, pipelineID) {
					writeError(w, http.StatusNotFound, "pipeline not found")
					return
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventPipelineDeleted,
						TenantID: tenant,
						Data:     map[string]string{"pipeline_id": pipelineID},
					})
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		if parts[1] != "runs" {
			writeError(w, http.StatusNotFound, "unknown sub-path")
			return
		}

		// /agent-pipelines/{id}/runs
		if len(parts) == 2 {
			switch r.Method {
			case http.MethodGet:
				runs := pipelineMgr.ListRuns(tenant, pipelineID)
				if runs == nil {
					runs = []pipeline.Run{}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"runs": runs, "count": len(runs)})
			case http.MethodPost:
				var body struct {
					Input map[string]any `json:"input"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
					writeError(w, http.StatusBadRequest, "invalid body")
					return
				}
				run, stage, err := pipelineMgr.StartRun(tenant, pipelineID, body.Input)
				if err != nil {
					if strings.Contains(err.Error(), "not found") {
						writeError(w, http.StatusNotFound, err.Error())
					} else {
						writeError(w, http.StatusBadRequest, err.Error())
					}
					return
				}
				stagePayload := make(map[string]any)
				for k, v := range stage.Payload {
					stagePayload[k] = v
				}
				for k, v := range body.Input {
					stagePayload[k] = v
				}
				agentID := stage.AgentID
				if agentID == "" {
					best, ok := agentReg.FindBest(tenant, stage.Capability)
					if !ok {
						writeError(w, http.StatusServiceUnavailable, "no healthy agent available for capability: "+stage.Capability)
						return
					}
					agentID = best.ID
				}
				j := jobQueue.SubmitWithOpts(tenant, agentID, stage.Capability, stagePayload, jobs.SubmitOptions{})
				run, _ = pipelineMgr.RecordStageJob(tenant, run.ID, j.ID)
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventPipelineRunStarted,
						TenantID: tenant,
						Data: map[string]any{
							"run_id":      run.ID,
							"pipeline_id": pipelineID,
							"stage":       0,
							"job_id":      j.ID,
						},
					})
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"run": run, "job": j})
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
			return
		}

		runID := parts[2]

		// /agent-pipelines/{id}/runs/{runID}
		if len(parts) == 3 {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			run, ok := pipelineMgr.GetRun(tenant, runID)
			if !ok {
				writeError(w, http.StatusNotFound, "run not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(run)
			return
		}

		// /agent-pipelines/{id}/runs/{runID}/advance
		if parts[3] == "advance" && r.Method == http.MethodPost {
			var body struct {
				Result string `json:"result"`
				Failed bool   `json:"failed"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			run, nextStage, err := pipelineMgr.AdvanceRun(tenant, runID, body.Failed)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					writeError(w, http.StatusNotFound, err.Error())
				} else {
					writeError(w, http.StatusBadRequest, err.Error())
				}
				return
			}
			if nextStage == nil {
				evType := events.EventPipelineRunDone
				if run.Status == pipeline.RunStatusFailed {
					evType = events.EventPipelineRunFailed
				}
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     evType,
						TenantID: tenant,
						Data:     map[string]any{"run_id": run.ID, "pipeline_id": pipelineID},
					})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"run": run, "job": nil})
				return
			}
			stagePayload := make(map[string]any)
			for k, v := range nextStage.Payload {
				stagePayload[k] = v
			}
			if body.Result != "" {
				stagePayload["_prev_result"] = body.Result
			}
			agentID := nextStage.AgentID
			if agentID == "" {
				best, ok := agentReg.FindBest(tenant, nextStage.Capability)
				if !ok {
					writeError(w, http.StatusServiceUnavailable, "no healthy agent available for capability: "+nextStage.Capability)
					return
				}
				agentID = best.ID
			}
			j := jobQueue.SubmitWithOpts(tenant, agentID, nextStage.Capability, stagePayload, jobs.SubmitOptions{})
			run, _ = pipelineMgr.RecordStageJob(tenant, run.ID, j.ID)
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventPipelineRunAdvanced,
					TenantID: tenant,
					Data: map[string]any{
						"run_id":      run.ID,
						"pipeline_id": pipelineID,
						"stage":       run.CurrentStage,
						"job_id":      j.ID,
					},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"run": run, "job": j})
			return
		}

		writeError(w, http.StatusNotFound, "unknown sub-path")
	})

	// GET/POST /schedules
	mux.HandleFunc("/schedules", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		switch r.Method {
		case http.MethodGet:
			all := sched.List()
			// filter to tenant
			filtered := all[:0]
			for _, s := range all {
				if s.Tenant == tenant || tenant == "" {
					filtered = append(filtered, s)
				}
			}
			if filtered == nil {
				filtered = []scheduler.Schedule{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"schedules": filtered, "count": len(filtered)})
		case http.MethodPost:
			var body struct {
				Name        string         `json:"name"`
				AgentID     string         `json:"agent_id"`
				Capability  string         `json:"capability"`
				Payload     map[string]any `json:"payload"`
				IntervalSec int            `json:"interval_seconds"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			if body.IntervalSec <= 0 {
				writeError(w, http.StatusBadRequest, "interval_seconds must be > 0")
				return
			}
			s, err := sched.Add(body.Name, tenant, body.AgentID, body.Capability, body.Payload, time.Duration(body.IntervalSec)*time.Second)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{Type: "schedule.created", TenantID: tenant, Timestamp: time.Now().UTC(), Data: map[string]any{"id": s.ID, "name": s.Name}})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(s)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// GET/PATCH/DELETE /schedules/{id}
	mux.HandleFunc("/schedules/", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load tenant")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/schedules/")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing schedule id")
			return
		}
		s, ok := sched.Get(id)
		if !ok || (tenant != "" && s.Tenant != tenant) {
			writeError(w, http.StatusNotFound, "schedule not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(s)
		case http.MethodPatch:
			var body struct {
				Name        *string         `json:"name"`
				AgentID     *string         `json:"agent_id"`
				Capability  *string         `json:"capability"`
				Payload     *map[string]any `json:"payload"`
				IntervalSec *int            `json:"interval_seconds"`
				Active      *bool           `json:"active"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid body")
				return
			}
			params := scheduler.UpdateParams{
				Name:       body.Name,
				AgentID:    body.AgentID,
				Capability: body.Capability,
				Payload:    body.Payload,
				Active:     body.Active,
			}
			if body.IntervalSec != nil {
				if *body.IntervalSec <= 0 {
					writeError(w, http.StatusBadRequest, "interval_seconds must be > 0")
					return
				}
				interval := time.Duration(*body.IntervalSec) * time.Second
				params.Interval = &interval
			}
			updated, ok, err := sched.Update(tenant, id, params)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if !ok {
				writeError(w, http.StatusNotFound, "schedule not found")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:      "schedule.updated",
					TenantID:  tenant,
					Timestamp: time.Now().UTC(),
					Data: map[string]any{
						"id":       updated.ID,
						"name":     updated.Name,
						"active":   updated.Active,
						"interval": updated.Interval,
					},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(updated)
		case http.MethodDelete:
			if !sched.Delete(id) {
				writeError(w, http.StatusNotFound, "schedule not found")
				return
			}
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:      "schedule.deleted",
					TenantID:  tenant,
					Timestamp: time.Now().UTC(),
					Data:      map[string]any{"id": s.ID, "name": s.Name},
				})
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
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

	// ── SaaS: self-service signup ─────────────────────────────────────────────
	// POST /auth/signup — create a new tenant + initial API key.
	// Body: {"tenant_id":"acme","name":"Acme Corp","email":"ops@acme.com","plan":"free"}
	// Returns: {"tenant_id","api_key","plan","quota"} — store the key, it is shown once.
	mux.HandleFunc("/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			TenantID string        `json:"tenant_id"`
			Name     string        `json:"name"`
			Email    string        `json:"email"`
			Plan     billing.Plan  `json:"plan"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		req.TenantID = strings.ToLower(strings.TrimSpace(req.TenantID))
		if req.TenantID == "" {
			writeError(w, http.StatusBadRequest, "tenant_id is required")
			return
		}
		if strings.TrimSpace(req.Email) == "" {
			writeError(w, http.StatusBadRequest, "email is required")
			return
		}
		if req.Plan == "" {
			req.Plan = billing.PlanFree
		}
		if req.Plan != billing.PlanFree && req.Plan != billing.PlanPro && req.Plan != billing.PlanEnterprise {
			writeError(w, http.StatusBadRequest, "plan must be free, pro, or enterprise")
			return
		}
		// Reject if profile already exists.
		if _, exists := meter.GetProfile(req.TenantID); exists {
			writeError(w, http.StatusConflict, "tenant already registered")
			return
		}
		// Ensure BoltDB tenant bucket exists.
		if err := db.EnsureTenant(req.TenantID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to provision tenant")
			return
		}
		quota := billing.QuotaFor(req.Plan)
		profile := billing.TenantProfile{
			TenantID:  req.TenantID,
			Name:      req.Name,
			Email:     req.Email,
			Plan:      req.Plan,
			Quota:     quota,
			CreatedAt: time.Now().UTC(),
		}
		if err := meter.SaveProfile(profile); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save profile")
			return
		}
		// Generate first API key.
		sk, err := meter.GenerateKey(req.TenantID, "default")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate api key")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": req.TenantID,
			"api_key":   sk.Key, // shown once
			"plan":      req.Plan,
			"quota":     quota,
		})
	})

	// ── SaaS: tenant self-service ─────────────────────────────────────────────
	// GET  /tenant/me — view own profile, plan, and today's usage.
	// PATCH /tenant/me — update name or email.
	mux.HandleFunc("/tenant/me", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil || tenant == "" {
			writeError(w, http.StatusUnauthorized, "authenticated tenant required")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			profile, _ := meter.GetProfile(tenant)
			if profile.TenantID == "" {
				profile = billing.TenantProfile{TenantID: tenant, Plan: billing.PlanFree, Quota: billing.QuotaFor(billing.PlanFree)}
			}
			usage := meter.Usage(tenant)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"profile": profile,
				"usage":   usage,
				"agents":  agentReg.CountByTenant(tenant),
			})
		case http.MethodPatch:
			var req struct {
				Name  string `json:"name,omitempty"`
				Email string `json:"email,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			profile, _ := meter.GetProfile(tenant)
			if profile.TenantID == "" {
				profile = billing.TenantProfile{TenantID: tenant, Plan: billing.PlanFree, Quota: billing.QuotaFor(billing.PlanFree), CreatedAt: time.Now().UTC()}
			}
			if req.Name != "" {
				profile.Name = req.Name
			}
			if req.Email != "" {
				profile.Email = req.Email
			}
			if err := meter.SaveProfile(profile); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to save profile")
				return
			}
			_ = json.NewEncoder(w).Encode(profile)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	// ── SaaS: sub-key management ──────────────────────────────────────────────
	// GET    /tenant/keys      — list sub-keys (secrets redacted).
	// POST   /tenant/keys      — create a new sub-key. Body: {"name":"ci-pipeline"}
	// DELETE /tenant/keys/{id} — revoke a sub-key.
	mux.HandleFunc("/tenant/keys", func(w http.ResponseWriter, r *http.Request) {
		_, tenant, err := getState(r)
		if err != nil || tenant == "" {
			writeError(w, http.StatusUnauthorized, "authenticated tenant required")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			keys, err := meter.ListKeys(tenant)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list keys")
				return
			}
			if keys == nil {
				keys = []billing.SubKey{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(keys), "keys": keys})
		case http.MethodPost:
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				req.Name = "key-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			}
			sk, err := meter.GenerateKey(tenant, req.Name)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to generate key")
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sk) // full secret shown once
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.HandleFunc("/tenant/keys/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, tenant, err := getState(r)
		if err != nil || tenant == "" {
			writeError(w, http.StatusUnauthorized, "authenticated tenant required")
			return
		}
		keyID := strings.TrimPrefix(r.URL.Path, "/tenant/keys/")
		if keyID == "" {
			writeError(w, http.StatusBadRequest, "key id is required")
			return
		}
		if err := meter.DeleteKey(tenant, keyID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete key")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// ── SaaS: usage / plan admin ──────────────────────────────────────────────
	// GET /admin/usage — global usage report across all tenants (admin only).
	// GET /admin/usage?tenant=X — usage for a specific tenant.
	mux.HandleFunc("/admin/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		tenantFilter := r.URL.Query().Get("tenant")
		var tenants []string
		if tenantFilter != "" {
			tenants = []string{tenantFilter}
		} else {
			names, err := db.TenantNames()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to list tenants")
				return
			}
			tenants = names
		}

		type tenantReport struct {
			TenantID string                   `json:"tenant_id"`
			Plan     billing.Plan             `json:"plan"`
			Quota    billing.Quota            `json:"quota"`
			Usage    map[string]int64         `json:"usage_today"`
			Agents   int                      `json:"agents_live"`
		}
		reports := make([]tenantReport, 0, len(tenants))
		for _, t := range tenants {
			profile, _ := meter.GetProfile(t)
			if profile.TenantID == "" {
				profile = billing.TenantProfile{TenantID: t, Plan: billing.PlanFree, Quota: billing.QuotaFor(billing.PlanFree)}
			}
			reports = append(reports, tenantReport{
				TenantID: t,
				Plan:     profile.Plan,
				Quota:    profile.Quota,
				Usage:    meter.Usage(t),
				Agents:   agentReg.CountByTenant(t),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"date":    time.Now().UTC().Format("2006-01-02"),
			"tenants": reports,
			"count":   len(reports),
		})
	})

	// PATCH /admin/tenants/{id}/plan — upgrade/downgrade a tenant's plan (admin).
	// Body: {"plan":"pro"}
	mux.HandleFunc("/admin/tenants/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/admin/tenants/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[1] != "plan" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		targetTenant := parts[0]
		var req struct {
			Plan billing.Plan `json:"plan"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Plan != billing.PlanFree && req.Plan != billing.PlanPro && req.Plan != billing.PlanEnterprise {
			writeError(w, http.StatusBadRequest, "plan must be free, pro, or enterprise")
			return
		}
		profile, _ := meter.GetProfile(targetTenant)
		if profile.TenantID == "" {
			profile = billing.TenantProfile{TenantID: targetTenant, CreatedAt: time.Now().UTC()}
		}
		profile.Plan = req.Plan
		profile.Quota = billing.QuotaFor(req.Plan)
		if err := meter.SaveProfile(profile); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update plan")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(profile)
	})

	// ── AI inference ──────────────────────────────────────────────────────────
	// POST /ai/infer         — run an inference request.
	//   mode=direct (default): call Claude API synchronously; returns result inline.
	//   mode=async:            dispatch an "inference" job to the best available
	//                          agent and return the job record immediately.
	//   stream=true:           proxy Claude's SSE stream to the caller (direct only).
	// GET  /ai/agents        — list agents that advertise the "inference" capability.
	// POST /ai/chat/{id}     — send a user message to a persistent chat session.
	// GET  /ai/chat/{id}     — retrieve chat session history.
	// DELETE /ai/chat/{id}   — delete a chat session.
	// POST /ai/analyze       — ask Claude to analyze live cluster health.
	{
		aiClient := ai.New(os.Getenv("ANTHROPIC_API_KEY"))

		mux.HandleFunc("/ai/infer", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}

			var req struct {
				Prompt    string `json:"prompt"`
				Model     string `json:"model,omitempty"`
				System    string `json:"system,omitempty"`
				MaxTokens int    `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Prompt) == "" {
				writeError(w, http.StatusBadRequest, "prompt is required")
				return
			}

			mode := r.URL.Query().Get("mode")
			if mode == "" {
				mode = "direct"
			}
			streamMode := r.URL.Query().Get("stream") == "true"

			// Streaming direct inference — SSE.
			if streamMode && mode == "direct" {
				if os.Getenv("ANTHROPIC_API_KEY") == "" {
					writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
					return
				}
				flusher, ok := w.(http.Flusher)
				if !ok {
					writeError(w, http.StatusInternalServerError, "streaming not supported")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")

				ch := aiClient.Stream(r.Context(), ai.StreamRequest{
					Messages:  []ai.Message{{Role: "user", Content: req.Prompt}},
					Model:     req.Model,
					System:    req.System,
					MaxTokens: req.MaxTokens,
				})
				for chunk := range ch {
					data, _ := json.Marshal(chunk)
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
					if chunk.Type == "stop" || chunk.Type == "error" {
						return
					}
				}
				return
			}

			w.Header().Set("Content-Type", "application/json")

			switch mode {
			case "direct":
				if os.Getenv("ANTHROPIC_API_KEY") == "" {
					writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
					return
				}
				result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
					Prompt:    req.Prompt,
					Model:     req.Model,
					System:    req.System,
					MaxTokens: req.MaxTokens,
				})
				if inferErr != nil {
					writeError(w, http.StatusBadGateway, inferErr.Error())
					return
				}
				meter.Record(tenant, billing.MetricAITokens, int64(result.InputTokens+result.OutputTokens))
				_ = json.NewEncoder(w).Encode(result)

			case "async":
				if jobQueue == nil {
					writeError(w, http.StatusServiceUnavailable, "job queue unavailable")
					return
				}
				best, ok := agentReg.FindBest(tenant, "inference")
				if !ok {
					writeError(w, http.StatusServiceUnavailable, "no healthy inference agent available")
					return
				}
				payload := map[string]any{
					"prompt":     req.Prompt,
					"model":      req.Model,
					"system":     req.System,
					"max_tokens": req.MaxTokens,
				}
				j := jobQueue.Submit(tenant, best.ID, "inference", payload)
				if eventBroker != nil {
					eventBroker.Publish(events.Event{
						Type:     events.EventJobCreated,
						TenantID: tenant,
						Data:     j,
					})
				}
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(j)

			default:
				writeError(w, http.StatusBadRequest, "mode must be direct or async")
			}
		})

		mux.HandleFunc("/ai/agents", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			list := agentReg.ListByCapability(tenant, "inference")
			if list == nil {
				list = []agent.Agent{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(list), "agents": list})
		})

		// POST /ai/infer?stream=true — SSE streaming inference (direct mode only).
		// Handled by the same /ai/infer route above; stream=true is detected there.
		// The handler below is a no-op placeholder — streaming is inline above.

		// ── chat sessions ─────────────────────────────────────────────────────
		// Sessions are stored in the KV store as JSON arrays of {role,content}
		// messages under the key "ai_chat_<id>". They expire after 24 hours of
		// inactivity (reset on every message).
		const chatTTL = 24 * time.Hour
		const chatPrefix = "ai_chat_"

		mux.HandleFunc("/ai/chat/", func(w http.ResponseWriter, r *http.Request) {
			if kvStore == nil {
				writeError(w, http.StatusServiceUnavailable, "kv store unavailable")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			rest := strings.TrimPrefix(r.URL.Path, "/ai/chat/")
			if rest == "" {
				writeError(w, http.StatusBadRequest, "session id is required")
				return
			}

			// GET /ai/chat/{id}/summary — condense session history.
			if strings.HasSuffix(rest, "/summary") {
				if r.Method != http.MethodGet {
					writeError(w, http.StatusMethodNotAllowed, "method not allowed")
					return
				}
				if os.Getenv("ANTHROPIC_API_KEY") == "" {
					writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
					return
				}
				sid := strings.TrimSuffix(rest, "/summary")
				entry, ok := kvStore.Get(tenant, chatPrefix+sid)
				if !ok {
					writeError(w, http.StatusNotFound, "session not found")
					return
				}
				var msgs []ai.Message
				if err := json.Unmarshal([]byte(entry.Value), &msgs); err != nil {
					writeError(w, http.StatusInternalServerError, "corrupt session")
					return
				}
				if len(msgs) == 0 {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sid, "summary": ""})
					return
				}
				var transcript strings.Builder
				for _, m := range msgs {
					fmt.Fprintf(&transcript, "%s: %s\n\n", strings.ToUpper(m.Role), m.Content)
				}
				result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
					Prompt:      "Summarise this conversation in 2–4 sentences, capturing the key points and outcomes:\n\n" + transcript.String(),
					System:      "You are a concise summariser. Write in third person.",
					CacheSystem: true,
				})
				if inferErr != nil {
					writeError(w, http.StatusBadGateway, inferErr.Error())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"session_id":    sid,
					"summary":       result.Content,
					"turns":         len(msgs) / 2,
					"model":         result.Model,
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
				})
				return
			}

			sessionID := rest
			kvKey := chatPrefix + sessionID

			loadHistory := func() ([]ai.Message, error) {
				entry, ok := kvStore.Get(tenant, kvKey)
				if !ok {
					return []ai.Message{}, nil
				}
				var msgs []ai.Message
				if err := json.Unmarshal([]byte(entry.Value), &msgs); err != nil {
					return nil, err
				}
				return msgs, nil
			}

			saveHistory := func(msgs []ai.Message) error {
				data, err := json.Marshal(msgs)
				if err != nil {
					return err
				}
				kvStore.Set(tenant, kvKey, string(data), chatTTL)
				return nil
			}

			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				history, err := loadHistory()
				if err != nil {
					writeError(w, http.StatusInternalServerError, "failed to load session")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"session_id": sessionID,
					"messages":   history,
					"count":      len(history),
				})

			case http.MethodPost:
				var req struct {
					Message   string `json:"message"`
					System    string `json:"system,omitempty"`
					Model     string `json:"model,omitempty"`
					MaxTokens int    `json:"max_tokens,omitempty"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				if strings.TrimSpace(req.Message) == "" {
					writeError(w, http.StatusBadRequest, "message is required")
					return
				}
				history, err := loadHistory()
				if err != nil {
					writeError(w, http.StatusInternalServerError, "failed to load session")
					return
				}
				history = append(history, ai.Message{Role: "user", Content: req.Message})

				result, inferErr := aiClient.InferMultiTurn(r.Context(), history, req.System, req.Model, req.MaxTokens)
				if inferErr != nil {
					writeError(w, http.StatusBadGateway, inferErr.Error())
					return
				}
				history = append(history, ai.Message{Role: "assistant", Content: result.Content})
				if err := saveHistory(history); err != nil {
					writeError(w, http.StatusInternalServerError, "failed to save session")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"session_id":    sessionID,
					"reply":         result.Content,
					"model":         result.Model,
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
					"turn":          len(history) / 2,
				})

			case http.MethodDelete:
				kvStore.Delete(tenant, kvKey)
				w.WriteHeader(http.StatusNoContent)

			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})

		// ── cluster analysis ──────────────────────────────────────────────────
		// POST /ai/analyze — gather live cluster state and ask Claude to analyse it.
		// Optional body: {"question": "...", "system": "...", "model": "..."}
		mux.HandleFunc("/ai/analyze", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			state, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}

			var req struct {
				Question  string `json:"question,omitempty"`
				System    string `json:"system,omitempty"`
				Model     string `json:"model,omitempty"`
				MaxTokens int    `json:"max_tokens,omitempty"`
			}
			// Body is optional — ignore decode errors.
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req)

			// Gather snapshot of cluster state.
			agents := agentReg.List(tenant)
			byCap := map[string]int{}
			byStatus := map[string]int{}
			for _, a := range agents {
				byStatus[string(a.Status)]++
				for _, c := range a.Capabilities {
					byCap[c]++
				}
			}
			routes := state.router.ListRoutes()
			var jobStats map[jobs.Status]int
			if jobQueue != nil {
				jobStats = jobQueue.CountByStatus(tenant)
			}

			snapshot, _ := json.Marshal(map[string]any{
				"tenant":        tenant,
				"agents_total":  len(agents),
				"agents_status": byStatus,
				"capabilities":  byCap,
				"routes_total":  len(routes),
				"jobs":          jobStats,
			})

			question := req.Question
			if question == "" {
				question = "Analyse the cluster health and highlight any concerns or recommendations."
			}
			systemPrompt := req.System
			if systemPrompt == "" {
				systemPrompt = "You are a Spectral Cloud operations assistant. You receive a JSON snapshot of the cluster state and provide a concise, actionable analysis."
			}

			prompt := fmt.Sprintf("Cluster snapshot:\n```json\n%s\n```\n\n%s", string(snapshot), question)

			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:    prompt,
				Model:     req.Model,
				System:    systemPrompt,
				MaxTokens: req.MaxTokens,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"analysis":      result.Content,
				"model":         result.Model,
				"input_tokens":  result.InputTokens,
				"output_tokens": result.OutputTokens,
				"snapshot":      json.RawMessage(snapshot),
			})
		})

		// ── smart routing ─────────────────────────────────────────────────────
		// POST /ai/route — describe a task in natural language; Claude picks the
		// best matching agent capability and submits a job automatically.
		// Body: {"task":"...", "payload":{...}, "model":"...", "max_tokens":N}
		mux.HandleFunc("/ai/route", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			if jobQueue == nil {
				writeError(w, http.StatusServiceUnavailable, "job queue unavailable")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			var req struct {
				Task      string         `json:"task"`
				Payload   map[string]any `json:"payload,omitempty"`
				Model     string         `json:"model,omitempty"`
				MaxTokens int            `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Task) == "" {
				writeError(w, http.StatusBadRequest, "task is required")
				return
			}

			// Collect unique capabilities from live agents.
			agents := agentReg.List(tenant)
			capSet := map[string]struct{}{}
			for _, a := range agents {
				for _, c := range a.Capabilities {
					capSet[c] = struct{}{}
				}
			}
			if len(capSet) == 0 {
				writeError(w, http.StatusServiceUnavailable, "no agents registered for this tenant")
				return
			}
			capList := make([]string, 0, len(capSet))
			for c := range capSet {
				capList = append(capList, c)
			}
			sort.Strings(capList)

			routePrompt := fmt.Sprintf(
				"Available agent capabilities: %s\n\nTask: %s\n\nReply with ONLY the single capability name from the list above that best matches this task. No explanation.",
				strings.Join(capList, ", "), req.Task,
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:    routePrompt,
				Model:     req.Model,
				System:    "You are a job router. Select the single best capability for the given task.",
				MaxTokens: 32,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			chosen := strings.TrimSpace(strings.ToLower(result.Content))
			// Validate Claude picked a real capability.
			if _, ok := capSet[chosen]; !ok {
				writeError(w, http.StatusBadGateway, fmt.Sprintf("model chose unknown capability %q", chosen))
				return
			}
			best, ok := agentReg.FindBest(tenant, chosen)
			if !ok {
				writeError(w, http.StatusServiceUnavailable, "no healthy agent available for capability: "+chosen)
				return
			}
			payload := req.Payload
			if payload == nil {
				payload = map[string]any{"task": req.Task}
			}
			j := jobQueue.Submit(tenant, best.ID, chosen, payload)
			if eventBroker != nil {
				eventBroker.Publish(events.Event{
					Type:     events.EventJobCreated,
					TenantID: tenant,
					Data:     j,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"chosen_capability": chosen,
				"agent_id":          best.ID,
				"job":               j,
				"routing_model":     result.Model,
			})
		})

		// ── structured extraction ─────────────────────────────────────────────
		// POST /ai/extract — extract structured JSON from text using Claude.
		// Body: {"text":"...", "schema":"describe the fields to extract", "model":"...", "max_tokens":N}
		mux.HandleFunc("/ai/extract", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Text      string `json:"text"`
				Schema    string `json:"schema"`
				Model     string `json:"model,omitempty"`
				MaxTokens int    `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Text) == "" {
				writeError(w, http.StatusBadRequest, "text is required")
				return
			}
			if strings.TrimSpace(req.Schema) == "" {
				writeError(w, http.StatusBadRequest, "schema is required")
				return
			}
			prompt := fmt.Sprintf(
				"Extract structured data from the text below. Return ONLY valid JSON matching this schema:\n%s\n\nText:\n%s",
				req.Schema, req.Text,
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:    prompt,
				Model:     req.Model,
				System:    "You are a data extraction engine. Return only valid JSON, no markdown, no explanation.",
				MaxTokens: req.MaxTokens,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			// Try to parse Claude's output as JSON.
			var extracted any
			w.Header().Set("Content-Type", "application/json")
			if err := json.Unmarshal([]byte(result.Content), &extracted); err != nil {
				// Return raw string if not valid JSON.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"raw":           result.Content,
					"parsed":        nil,
					"model":         result.Model,
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"extracted":     extracted,
				"model":         result.Model,
				"input_tokens":  result.InputTokens,
				"output_tokens": result.OutputTokens,
			})
		})

		// ── chat session index ────────────────────────────────────────────────
		// GET /ai/sessions — list active chat session IDs for this tenant.
		mux.HandleFunc("/ai/sessions", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if kvStore == nil {
				writeError(w, http.StatusServiceUnavailable, "kv store unavailable")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			entries := kvStore.List(tenant, chatPrefix)
			type sessionSummary struct {
				SessionID string `json:"session_id"`
				Turns     int    `json:"turns"`
				UpdatedAt string `json:"updated_at"`
			}
			summaries := make([]sessionSummary, 0, len(entries))
			for _, e := range entries {
				sessionID := strings.TrimPrefix(e.Key, chatPrefix)
				var msgs []ai.Message
				turns := 0
				if err := json.Unmarshal([]byte(e.Value), &msgs); err == nil {
					turns = len(msgs) / 2
				}
				summaries = append(summaries, sessionSummary{
					SessionID: sessionID,
					Turns:     turns,
					UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(summaries), "sessions": summaries})
		})

		// ── prompt templates ──────────────────────────────────────────────────
		// Templates are named prompt skeletons stored in KV.
		// A template has: system, prompt (with {{var}} placeholders), model, max_tokens.
		// GET    /ai/templates           — list all templates
		// POST   /ai/templates           — create/update a template (body: template JSON)
		// GET    /ai/templates/{name}    — fetch a template
		// DELETE /ai/templates/{name}    — delete a template
		// POST   /ai/templates/{name}/run — render vars and call Claude
		const tmplPrefix = "ai_tmpl_"

		mux.HandleFunc("/ai/templates", func(w http.ResponseWriter, r *http.Request) {
			if kvStore == nil {
				writeError(w, http.StatusServiceUnavailable, "kv store unavailable")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				entries := kvStore.List(tenant, tmplPrefix)
				type tmplMeta struct {
					Name      string `json:"name"`
					UpdatedAt string `json:"updated_at"`
				}
				metas := make([]tmplMeta, 0, len(entries))
				for _, e := range entries {
					metas = append(metas, tmplMeta{
						Name:      strings.TrimPrefix(e.Key, tmplPrefix),
						UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339),
					})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"count": len(metas), "templates": metas})

			case http.MethodPost:
				var tmpl struct {
					Name      string `json:"name"`
					System    string `json:"system,omitempty"`
					Prompt    string `json:"prompt"`
					Model     string `json:"model,omitempty"`
					MaxTokens int    `json:"max_tokens,omitempty"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&tmpl); err != nil {
					writeError(w, http.StatusBadRequest, "invalid request body")
					return
				}
				if strings.TrimSpace(tmpl.Name) == "" {
					writeError(w, http.StatusBadRequest, "name is required")
					return
				}
				if strings.TrimSpace(tmpl.Prompt) == "" {
					writeError(w, http.StatusBadRequest, "prompt is required")
					return
				}
				data, _ := json.Marshal(tmpl)
				kvStore.Set(tenant, tmplPrefix+tmpl.Name, string(data), 0)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(tmpl)

			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})

		mux.HandleFunc("/ai/templates/", func(w http.ResponseWriter, r *http.Request) {
			if kvStore == nil {
				writeError(w, http.StatusServiceUnavailable, "kv store unavailable")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" && r.Method == http.MethodPost {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}

			rest := strings.TrimPrefix(r.URL.Path, "/ai/templates/")
			// POST /ai/templates/{name}/run
			if strings.HasSuffix(rest, "/run") && r.Method == http.MethodPost {
				name := strings.TrimSuffix(rest, "/run")
				entry, ok := kvStore.Get(tenant, tmplPrefix+name)
				if !ok {
					writeError(w, http.StatusNotFound, "template not found")
					return
				}
				var tmpl struct {
					System    string `json:"system"`
					Prompt    string `json:"prompt"`
					Model     string `json:"model"`
					MaxTokens int    `json:"max_tokens"`
				}
				if err := json.Unmarshal([]byte(entry.Value), &tmpl); err != nil {
					writeError(w, http.StatusInternalServerError, "corrupt template")
					return
				}
				var vars map[string]string
				_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&vars)

				rendered := tmpl.Prompt
				for k, v := range vars {
					rendered = strings.ReplaceAll(rendered, "{{"+k+"}}", v)
				}
				result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
					Prompt:    rendered,
					Model:     tmpl.Model,
					System:    tmpl.System,
					MaxTokens: tmpl.MaxTokens,
				})
				if inferErr != nil {
					writeError(w, http.StatusBadGateway, inferErr.Error())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"template":      name,
					"rendered":      rendered,
					"content":       result.Content,
					"model":         result.Model,
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
				})
				return
			}

			// /ai/templates/{name} — GET / DELETE
			name := rest
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				entry, ok := kvStore.Get(tenant, tmplPrefix+name)
				if !ok {
					writeError(w, http.StatusNotFound, "template not found")
					return
				}
				w.Write([]byte(entry.Value)) //nolint:errcheck
			case http.MethodDelete:
				if !kvStore.Delete(tenant, tmplPrefix+name) {
					writeError(w, http.StatusNotFound, "template not found")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			}
		})

		// ── LLM-as-judge ─────────────────────────────────────────────────────
		// POST /ai/judge — score text against criteria on a 0–10 scale.
		// Body: {"text":"...", "criteria":"...", "model":"...", "max_tokens":N}
		// Returns: {"score":8, "reasoning":"...", "model":"..."}
		mux.HandleFunc("/ai/judge", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Text      string `json:"text"`
				Criteria  string `json:"criteria"`
				Model     string `json:"model,omitempty"`
				MaxTokens int    `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Text) == "" {
				writeError(w, http.StatusBadRequest, "text is required")
				return
			}
			if strings.TrimSpace(req.Criteria) == "" {
				writeError(w, http.StatusBadRequest, "criteria is required")
				return
			}
			prompt := fmt.Sprintf(
				"Evaluate the following text against the criteria and respond with ONLY valid JSON in this exact format:\n"+
					`{"score": <integer 0-10>, "reasoning": "<one sentence>"}`+"\n\n"+
					"Criteria: %s\n\nText:\n%s",
				req.Criteria, req.Text,
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:      prompt,
				Model:       req.Model,
				System:      "You are an objective evaluator. Return only valid JSON.",
				MaxTokens:   req.MaxTokens,
				CacheSystem: true,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			var verdict struct {
				Score     int    `json:"score"`
				Reasoning string `json:"reasoning"`
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.Unmarshal([]byte(result.Content), &verdict); err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"raw": result.Content, "model": result.Model})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"score":            verdict.Score,
				"reasoning":        verdict.Reasoning,
				"model":            result.Model,
				"input_tokens":     result.InputTokens,
				"output_tokens":    result.OutputTokens,
				"cache_read":       result.CacheReadTokens,
				"cache_write":      result.CacheWriteTokens,
			})
		})

		// ── candidate re-ranking ──────────────────────────────────────────────
		// POST /ai/rerank — rank a list of candidates by relevance to a query.
		// Body: {"query":"...", "candidates":["a","b","c"], "model":"...", "max_tokens":N}
		// Returns: {"ranked":[{"index":2,"text":"c","score":9},...],"model":"..."}
		mux.HandleFunc("/ai/rerank", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Query      string   `json:"query"`
				Candidates []string `json:"candidates"`
				Model      string   `json:"model,omitempty"`
				MaxTokens  int      `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Query) == "" {
				writeError(w, http.StatusBadRequest, "query is required")
				return
			}
			if len(req.Candidates) == 0 {
				writeError(w, http.StatusBadRequest, "candidates is required")
				return
			}
			var numbered strings.Builder
			for i, c := range req.Candidates {
				fmt.Fprintf(&numbered, "%d. %s\n", i, c)
			}
			prompt := fmt.Sprintf(
				"Rank the following candidates by relevance to the query.\n"+
					"Return ONLY a JSON array of objects ordered from most to least relevant:\n"+
					`[{"index":<original 0-based index>,"text":"<candidate text>","score":<1-10>}]`+"\n\n"+
					"Query: %s\n\nCandidates:\n%s",
				req.Query, numbered.String(),
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:      prompt,
				Model:       req.Model,
				System:      "You are a relevance ranking engine. Return only valid JSON.",
				MaxTokens:   req.MaxTokens,
				CacheSystem: true,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			var ranked []map[string]any
			w.Header().Set("Content-Type", "application/json")
			if err := json.Unmarshal([]byte(result.Content), &ranked); err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"raw": result.Content, "model": result.Model})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ranked":        ranked,
				"model":         result.Model,
				"input_tokens":  result.InputTokens,
				"output_tokens": result.OutputTokens,
			})
		})

		// ── AI diff ──────────────────────────────────────────────────────────
		// POST /ai/diff — explain the differences between two texts using Claude.
		// Body: {"before":"...", "after":"...", "context":"...", "model":"...", "max_tokens":N}
		mux.HandleFunc("/ai/diff", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Before    string `json:"before"`
				After     string `json:"after"`
				Context   string `json:"context,omitempty"`
				Model     string `json:"model,omitempty"`
				MaxTokens int    `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.Before == "" || req.After == "" {
				writeError(w, http.StatusBadRequest, "before and after are required")
				return
			}
			contextLine := ""
			if req.Context != "" {
				contextLine = "Context: " + req.Context + "\n\n"
			}
			prompt := fmt.Sprintf(
				"%sExplain the differences between the BEFORE and AFTER versions. "+
					"Be specific and concise. Highlight what changed, why it matters, and any risks.\n\n"+
					"BEFORE:\n%s\n\nAFTER:\n%s",
				contextLine, req.Before, req.After,
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:    prompt,
				Model:     req.Model,
				System:    "You are a precise diff analyst. Focus on meaningful changes, not formatting.",
				MaxTokens: req.MaxTokens,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"explanation":   result.Content,
				"model":         result.Model,
				"input_tokens":  result.InputTokens,
				"output_tokens": result.OutputTokens,
			})
		})

		// ── available models ──────────────────────────────────────────────────
		// GET /ai/models — list Claude models available for use.
		mux.HandleFunc("/ai/models", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			type modelInfo struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				MaxTokens    int    `json:"max_tokens"`
				Description  string `json:"description"`
				InputCaching bool   `json:"prompt_caching"`
			}
			models := []modelInfo{
				{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", MaxTokens: 32000, Description: "Most capable model for complex tasks", InputCaching: true},
				{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", MaxTokens: 16000, Description: "Best balance of intelligence and speed", InputCaching: true},
				{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", MaxTokens: 8192, Description: "Fastest and most compact model", InputCaching: true},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models, "count": len(models)})
		})

		// ── text classification ───────────────────────────────────────────────
		// POST /ai/classify — assign one or more labels from a fixed set to text.
		// Body: {"text":"...", "labels":["positive","negative","neutral"],
		//        "multi_label":false, "model":"...", "max_tokens":N}
		mux.HandleFunc("/ai/classify", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Text       string   `json:"text"`
				Labels     []string `json:"labels"`
				MultiLabel bool     `json:"multi_label"`
				Model      string   `json:"model,omitempty"`
				MaxTokens  int      `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Text) == "" {
				writeError(w, http.StatusBadRequest, "text is required")
				return
			}
			if len(req.Labels) == 0 {
				writeError(w, http.StatusBadRequest, "labels is required")
				return
			}
			mode := "exactly one label"
			if req.MultiLabel {
				mode = "one or more labels (as a JSON array)"
			}
			prompt := fmt.Sprintf(
				"Classify the following text. Available labels: %s\n\n"+
					"Return ONLY valid JSON: if multi-label, return {\"labels\":[...],\"confidence\":{\"label\":0-1,...}}; "+
					"otherwise return {\"label\":\"...\",\"confidence\":0-1}.\n"+
					"Select %s.\n\nText:\n%s",
				strings.Join(req.Labels, ", "), mode, req.Text,
			)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:      prompt,
				Model:       req.Model,
				System:      "You are a text classification engine. Return only valid JSON.",
				MaxTokens:   req.MaxTokens,
				CacheSystem: true,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			var parsed any
			w.Header().Set("Content-Type", "application/json")
			if err := json.Unmarshal([]byte(result.Content), &parsed); err != nil {
				_ = json.NewEncoder(w).Encode(map[string]any{"raw": result.Content, "model": result.Model})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result":        parsed,
				"model":         result.Model,
				"input_tokens":  result.InputTokens,
				"output_tokens": result.OutputTokens,
			})
		})

		// ── translation ───────────────────────────────────────────────────────
		// POST /ai/translate — translate text between languages.
		// Body: {"text":"...", "target_language":"French", "source_language":"auto",
		//        "model":"...", "max_tokens":N}
		mux.HandleFunc("/ai/translate", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			var req struct {
				Text           string `json:"text"`
				TargetLanguage string `json:"target_language"`
				SourceLanguage string `json:"source_language,omitempty"`
				Model          string `json:"model,omitempty"`
				MaxTokens      int    `json:"max_tokens,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Text) == "" {
				writeError(w, http.StatusBadRequest, "text is required")
				return
			}
			if strings.TrimSpace(req.TargetLanguage) == "" {
				writeError(w, http.StatusBadRequest, "target_language is required")
				return
			}
			sourceLine := ""
			if req.SourceLanguage != "" && req.SourceLanguage != "auto" {
				sourceLine = fmt.Sprintf(" from %s", req.SourceLanguage)
			}
			prompt := fmt.Sprintf("Translate the following text%s to %s. Return only the translated text, nothing else.\n\n%s",
				sourceLine, req.TargetLanguage, req.Text)
			result, inferErr := aiClient.Infer(r.Context(), ai.InferRequest{
				Prompt:    prompt,
				Model:     req.Model,
				System:    "You are a professional translator. Return only the translated text.",
				MaxTokens: req.MaxTokens,
			})
			if inferErr != nil {
				writeError(w, http.StatusBadGateway, inferErr.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"translation":     result.Content,
				"target_language": req.TargetLanguage,
				"model":           result.Model,
				"input_tokens":    result.InputTokens,
				"output_tokens":   result.OutputTokens,
			})
		})

		// ── agentic loop ──────────────────────────────────────────────────────
		// POST /ai/loop — give Claude a goal; it autonomously calls built-in
		// Spectral Cloud tools (list_agents, submit_job, get_job, list_routes)
		// until the goal is achieved or max_iterations is reached.
		// Body: {"goal":"...", "model":"...", "max_tokens":N, "max_iterations":10}
		mux.HandleFunc("/ai/loop", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if os.Getenv("ANTHROPIC_API_KEY") == "" {
				writeError(w, http.StatusServiceUnavailable, "ANTHROPIC_API_KEY not configured")
				return
			}
			if jobQueue == nil {
				writeError(w, http.StatusServiceUnavailable, "job queue unavailable")
				return
			}
			_, tenant, err := getState(r)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load tenant")
				return
			}
			var req struct {
				Goal          string `json:"goal"`
				Model         string `json:"model,omitempty"`
				MaxTokens     int    `json:"max_tokens,omitempty"`
				MaxIterations int    `json:"max_iterations,omitempty"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxBodyBytes))).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if strings.TrimSpace(req.Goal) == "" {
				writeError(w, http.StatusBadRequest, "goal is required")
				return
			}
			if req.MaxIterations <= 0 {
				req.MaxIterations = 10
			}

			// Define built-in tools Claude can call.
			tools := []ai.Tool{
				{
					Name:        "list_agents",
					Description: "List registered agents for this tenant with their capabilities and status.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"capability":{"type":"string","description":"Filter by capability (optional)"}}}`),
				},
				{
					Name:        "submit_job",
					Description: "Submit a job to the queue for an agent with a specific capability.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"capability":{"type":"string"},"payload":{"type":"object"}},"required":["capability"]}`),
				},
				{
					Name:        "get_job",
					Description: "Get the status and result of a job by ID.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"string"}},"required":["job_id"]}`),
				},
				{
					Name:        "list_routes",
					Description: "List the current routing table entries.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
				},
			}

			// Tool executor — runs the tool and returns a result string.
			execTool := func(name string, input json.RawMessage) string {
				var args map[string]any
				_ = json.Unmarshal(input, &args)

				switch name {
				case "list_agents":
					cap, _ := args["capability"].(string)
					var list []agent.Agent
					if cap != "" {
						list = agentReg.ListByCapability(tenant, cap)
					} else {
						list = agentReg.List(tenant)
					}
					b, _ := json.Marshal(list)
					return string(b)

				case "submit_job":
					cap, _ := args["capability"].(string)
					if cap == "" {
						return `{"error":"capability is required"}`
					}
					payload, _ := args["payload"].(map[string]any)
					if payload == nil {
						payload = map[string]any{}
					}
					best, ok := agentReg.FindBest(tenant, cap)
					if !ok {
						return fmt.Sprintf(`{"error":"no healthy agent available for capability %q"}`, cap)
					}
					j := jobQueue.Submit(tenant, best.ID, cap, payload)
					if eventBroker != nil {
						eventBroker.Publish(events.Event{Type: events.EventJobCreated, TenantID: tenant, Data: j})
					}
					b, _ := json.Marshal(j)
					return string(b)

				case "get_job":
					id, _ := args["job_id"].(string)
					if id == "" {
						return `{"error":"job_id is required"}`
					}
					j, ok := jobQueue.Get(id)
					if !ok {
						return `{"error":"job not found"}`
					}
					b, _ := json.Marshal(j)
					return string(b)

				case "list_routes":
					state, _, _ := getState(r)
					if state == nil {
						return `{"error":"state unavailable"}`
					}
					b, _ := json.Marshal(state.router.ListRoutes())
					return string(b)

				default:
					return fmt.Sprintf(`{"error":"unknown tool %q"}`, name)
				}
			}

			type stepRecord struct {
				Iteration int    `json:"iteration"`
				Tool      string `json:"tool,omitempty"`
				Input     any    `json:"input,omitempty"`
				Result    string `json:"result,omitempty"`
				Text      string `json:"text,omitempty"`
			}

			history := []ai.Message{{Role: "user", Content: req.Goal}}
			var trace []stepRecord
			var finalText string
			totalIn, totalOut := 0, 0

			for iter := 0; iter < req.MaxIterations; iter++ {
				resp, loopErr := aiClient.InferWithTools(r.Context(), ai.ToolsRequest{
					Messages:  history,
					Tools:     tools,
					Model:     req.Model,
					System:    "You are an autonomous Spectral Cloud agent. Use the provided tools to accomplish the user's goal. When done, provide a concise summary of what you did.",
					MaxTokens: req.MaxTokens,
				})
				if loopErr != nil {
					writeError(w, http.StatusBadGateway, loopErr.Error())
					return
				}
				totalIn += resp.InputTokens
				totalOut += resp.OutputTokens

				if resp.StopReason == "end_turn" || len(resp.ToolUseCalls()) == 0 {
					finalText = resp.TextContent()
					if t := resp.TextContent(); t != "" {
						trace = append(trace, stepRecord{Iteration: iter, Text: t})
					}
					break
				}

				// Execute all tool calls in this turn.
				var results []ai.ToolResult
				for _, call := range resp.ToolUseCalls() {
					toolResult := execTool(call.Name, call.Input)
					results = append(results, ai.ToolResult{ToolUseID: call.ID, Content: toolResult})
					var inputParsed any
					_ = json.Unmarshal(call.Input, &inputParsed)
					trace = append(trace, stepRecord{
						Iteration: iter,
						Tool:      call.Name,
						Input:     inputParsed,
						Result:    toolResult,
					})
				}
				history = ai.AppendToolResults(history, resp.Content, results)
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"goal":          req.Goal,
				"result":        finalText,
				"trace":         trace,
				"iterations":    len(trace),
				"input_tokens":  totalIn,
				"output_tokens": totalOut,
			})
		})
	}

	// Serve the embedded web dashboard at /ui/.
	uiFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/ui/", http.StripPrefix("/ui", http.FileServer(http.FS(uiFS))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeError(w, http.StatusNotFound, "not found")
	})

	var limiter *ipLimiter
	if rateRPS > 0 && rateBurst > 0 {
		limiter = newIPLimiter(rate.Limit(rateRPS), rateBurst, 5*time.Minute)
	}
	var tenantLimiter *tenantLimiter
	if tenantRateRPS > 0 && tenantRateBurst > 0 {
		tenantLimiter = newTenantLimiter(rate.Limit(tenantRateRPS), tenantRateBurst, 10*time.Minute)
	}
	handler := withDynAuth(mux, meter, db, auth)
	handler = withAudit(handler, db, auth.defaultTenant)
	handler = withTenantRateLimit(handler, tenantLimiter, auth.defaultTenant)
	handler = withRateLimit(handler, limiter)
	handler = withMetrics(handler, requestsTotal)
	handler = withBillingMetrics(handler, meter, auth.defaultTenant)
	handler = withDuration(handler, requestDuration)
	handler = withCORS(handler, corsCfg)
	if enableAccessLog {
		handler = withLogger(handler)
	}
	handler = withRequestID(handler)
	handler = withRecover(handler)
	return handler
}

// withAudit records all mutating (non-GET) requests to the audit log.
func withAudit(next http.Handler, db *store.Store, defaultTenant string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		tenant, ok := tenantFromContext(r.Context())
		if !ok || strings.TrimSpace(tenant) == "" {
			tenant = defaultTenant
		}
		_ = db.AuditRecord(store.AuditEntry{
			Tenant: tenant,
			Method: r.Method,
			Path:   r.URL.Path,
			Status: rec.status,
		})
	})
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

// withBillingMetrics records per-tenant API call counts for SaaS metering.
func withBillingMetrics(next http.Handler, m *billing.Meter, defaultTenant string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		tenant, ok := tenantFromContext(r.Context())
		if !ok || tenant == "" {
			tenant = defaultTenant
		}
		if tenant != "" {
			m.Record(tenant, billing.MetricAPICalls, 1)
		}
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

// parseTagSelector parses a comma-separated "key=value" selector string into a
// map. Malformed pairs (missing "=") are silently skipped.
func parseTagSelector(s string) map[string]string {
	sel := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		sel[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return sel
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

func parseTagsStrict(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		parts := strings.SplitN(kv, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("tag must be in key:value format")
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
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

// withDynAuth wraps withAuth with a dynamic sub-key resolver.
// If the bearer token in the request matches a BoltDB-stored sub-key, the
// tenant is injected into the context so the inner withAuth sees it as
// pre-authenticated. Otherwise request falls through to normal auth.
func withDynAuth(next http.Handler, m *billing.Meter, db *store.Store, auth authConfig) http.Handler {
	inner := withAuth(next, auth)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract presented key.
		key := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			key = strings.TrimSpace(key)
		}
		if key != "" && strings.HasPrefix(key, "sk-") {
			// Look up across all tenants.
			names, _ := db.TenantNames()
			if sk, ok := m.GetKeyBySecret(names, key); ok {
				go m.TouchKey(sk.TenantID, sk.ID)
				ctx := withTenant(r.Context(), sk.TenantID)
				ctx = withRequestAuth(ctx, requestAuth{access: "tenant", tenant: sk.TenantID})
				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
}

func withAuth(next http.Handler, auth authConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if matchPathRules(path, r.Method, auth.publicRules) {
			next.ServeHTTP(w, r)
			return
		}
		if matchPathRules(path, r.Method, auth.adminRules) {
			if strings.TrimSpace(auth.adminKey) == "" &&
				strings.TrimSpace(auth.apiKey) == "" &&
				(!isWriteMethod(r.Method) || strings.TrimSpace(auth.adminWriteKey) == "") {
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
			if auth.defaultTenant != "" {
				r = r.WithContext(withTenant(r.Context(), auth.defaultTenant))
			}
			access := "admin"
			if isWriteMethod(r.Method) && strings.TrimSpace(auth.adminWriteKey) != "" {
				access = "admin-write"
			}
			r = r.WithContext(withRequestAuth(r.Context(), requestAuth{
				access: access,
				tenant: auth.defaultTenant,
			}))
			next.ServeHTTP(w, r)
			return
		}
		if auth.tenantKeys != nil || (isWriteMethod(r.Method) && auth.tenantWrite != nil) {
			manager := auth.tenantKeys
			access := "tenant"
			if isWriteMethod(r.Method) && auth.tenantWrite != nil {
				manager = auth.tenantWrite
				access = "tenant-write"
			}
			if manager == nil {
				next.ServeHTTP(w, r)
				return
			}
			tenant, ok := manager.TenantFromRequest(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			r = r.WithContext(withTenant(r.Context(), tenant))
			r = r.WithContext(withRequestAuth(r.Context(), requestAuth{
				access: access,
				tenant: tenant,
			}))
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
			r = r.WithContext(withRequestAuth(r.Context(), requestAuth{
				access: "write",
				tenant: auth.defaultTenant,
			}))
		} else if strings.TrimSpace(auth.apiKey) != "" {
			if !hasValidAPIKey(r, auth.apiKey) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if auth.defaultTenant != "" {
				r = r.WithContext(withTenant(r.Context(), auth.defaultTenant))
			}
			r = r.WithContext(withRequestAuth(r.Context(), requestAuth{
				access: "api",
				tenant: auth.defaultTenant,
			}))
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

func requestCredential(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		if token := strings.TrimSpace(header[7:]); token != "" {
			return token
		}
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

func resolvePresentedCredential(r *http.Request, auth authConfig) (requestAuth, bool) {
	token := requestCredential(r)
	if token == "" {
		return requestAuth{}, false
	}
	defaultTenant := strings.TrimSpace(auth.defaultTenant)
	if key := strings.TrimSpace(auth.adminWriteKey); key != "" && secureEquals(token, key) {
		return requestAuth{access: "admin-write", tenant: defaultTenant}, true
	}
	if key := strings.TrimSpace(auth.adminKey); key != "" && secureEquals(token, key) {
		return requestAuth{access: "admin", tenant: defaultTenant}, true
	}
	if auth.tenantWrite != nil {
		if tenant, ok := auth.tenantWrite.TenantFromKey(token); ok {
			return requestAuth{access: "tenant-write", tenant: tenant}, true
		}
	}
	if auth.tenantKeys != nil {
		if tenant, ok := auth.tenantKeys.TenantFromKey(token); ok {
			return requestAuth{access: "tenant", tenant: tenant}, true
		}
	}
	if key := strings.TrimSpace(auth.writeKey); key != "" && secureEquals(token, key) {
		return requestAuth{access: "write", tenant: defaultTenant}, true
	}
	if key := strings.TrimSpace(auth.apiKey); key != "" && secureEquals(token, key) {
		return requestAuth{access: "api", tenant: defaultTenant}, true
	}
	return requestAuth{}, false
}

func anyAuthConfigured(auth authConfig) bool {
	return strings.TrimSpace(auth.apiKey) != "" ||
		strings.TrimSpace(auth.writeKey) != "" ||
		strings.TrimSpace(auth.adminKey) != "" ||
		strings.TrimSpace(auth.adminWriteKey) != "" ||
		auth.tenantKeys != nil ||
		auth.tenantWrite != nil
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
