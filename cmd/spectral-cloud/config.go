package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// fileConfig mirrors the set of env vars that can be set via a JSON config
// file. Keys match the env var names in lower_snake_case. An empty value means
// "use the env var / built-in default".
type fileConfig struct {
	Port                string `json:"port,omitempty"`
	DataDir             string `json:"data_dir,omitempty"`
	DBPath              string `json:"db_path,omitempty"`
	APIKey              string `json:"api_key,omitempty"`
	AdminAPIKey         string `json:"admin_api_key,omitempty"`
	AdminWriteKey       string `json:"admin_write_key,omitempty"`
	WriteAPIKey         string `json:"write_api_key,omitempty"`
	DefaultTenant       string `json:"default_tenant,omitempty"`
	TenantKeys          string `json:"tenant_keys,omitempty"`
	TenantWriteKeys     string `json:"tenant_write_keys,omitempty"`
	RateLimitRPS        string `json:"rate_limit_rps,omitempty"`
	RateLimitBurst      string `json:"rate_limit_burst,omitempty"`
	TenantRateRPS       string `json:"tenant_rate_rps,omitempty"`
	TenantRateBurst     string `json:"tenant_rate_burst,omitempty"`
	TenantMaxBlocks     string `json:"tenant_max_blocks,omitempty"`
	TenantMaxRoutes     string `json:"tenant_max_routes,omitempty"`
	BackupInterval      string `json:"backup_interval,omitempty"`
	BackupDir           string `json:"backup_dir,omitempty"`
	BackupRetention     string `json:"backup_retention,omitempty"`
	BackupKeyB64        string `json:"backup_key_b64,omitempty"`
	CompactInterval     string `json:"compact_interval,omitempty"`
	CompactDir          string `json:"compact_dir,omitempty"`
	CompactRetention    string `json:"compact_retention,omitempty"`
	MaxBodyBytes        string `json:"max_body_bytes,omitempty"`
	TLSCertFile         string `json:"tls_cert_file,omitempty"`
	TLSKeyFile          string `json:"tls_key_file,omitempty"`
	BlockSigningKey     string `json:"block_signing_key,omitempty"`
	WebhookURL          string `json:"webhook_url,omitempty"`
	WebhookSecret       string `json:"webhook_secret,omitempty"`
	WebhookTimeout      string `json:"webhook_timeout,omitempty"`
	CORSAllowedOrigins  string `json:"cors_allowed_origins,omitempty"`
	CORSAllowedMethods  string `json:"cors_allowed_methods,omitempty"`
	CORSAllowedHeaders  string `json:"cors_allowed_headers,omitempty"`
	CORSMaxAge          string `json:"cors_max_age,omitempty"`
	AccessLog           string `json:"access_log,omitempty"`
	AgentTTLSeconds     string `json:"agent_ttl_seconds,omitempty"`
	MeshEnable          string `json:"mesh_enable,omitempty"`
	MeshTenant          string `json:"mesh_tenant,omitempty"`
	MeshGRPCAddr        string `json:"mesh_grpc_addr,omitempty"`
	AdminAllowRemote    string `json:"admin_allow_remote,omitempty"`
	AdminAllowlistCIDRs string `json:"admin_allowlist_cidrs,omitempty"`
	PublicPaths         string `json:"public_paths,omitempty"`
	AdminPaths          string `json:"admin_paths,omitempty"`
	CompactOnStart      string `json:"compact_on_start,omitempty"`
}

// fileConfigEnvMap maps each fileConfig field to its environment variable name.
var fileConfigEnvMap = map[string]func(*fileConfig) string{
	"PORT":                  func(c *fileConfig) string { return c.Port },
	"DATA_DIR":              func(c *fileConfig) string { return c.DataDir },
	"DB_PATH":               func(c *fileConfig) string { return c.DBPath },
	"API_KEY":               func(c *fileConfig) string { return c.APIKey },
	"ADMIN_API_KEY":         func(c *fileConfig) string { return c.AdminAPIKey },
	"ADMIN_WRITE_KEY":       func(c *fileConfig) string { return c.AdminWriteKey },
	"WRITE_API_KEY":         func(c *fileConfig) string { return c.WriteAPIKey },
	"DEFAULT_TENANT":        func(c *fileConfig) string { return c.DefaultTenant },
	"TENANT_KEYS":           func(c *fileConfig) string { return c.TenantKeys },
	"TENANT_WRITE_KEYS":     func(c *fileConfig) string { return c.TenantWriteKeys },
	"RATE_LIMIT_RPS":        func(c *fileConfig) string { return c.RateLimitRPS },
	"RATE_LIMIT_BURST":      func(c *fileConfig) string { return c.RateLimitBurst },
	"TENANT_RATE_RPS":       func(c *fileConfig) string { return c.TenantRateRPS },
	"TENANT_RATE_BURST":     func(c *fileConfig) string { return c.TenantRateBurst },
	"TENANT_MAX_BLOCKS":     func(c *fileConfig) string { return c.TenantMaxBlocks },
	"TENANT_MAX_ROUTES":     func(c *fileConfig) string { return c.TenantMaxRoutes },
	"BACKUP_INTERVAL":       func(c *fileConfig) string { return c.BackupInterval },
	"BACKUP_DIR":            func(c *fileConfig) string { return c.BackupDir },
	"BACKUP_RETENTION":      func(c *fileConfig) string { return c.BackupRetention },
	"BACKUP_KEY_B64":        func(c *fileConfig) string { return c.BackupKeyB64 },
	"COMPACT_INTERVAL":      func(c *fileConfig) string { return c.CompactInterval },
	"COMPACT_DIR":           func(c *fileConfig) string { return c.CompactDir },
	"COMPACT_RETENTION":     func(c *fileConfig) string { return c.CompactRetention },
	"MAX_BODY_BYTES":        func(c *fileConfig) string { return c.MaxBodyBytes },
	"TLS_CERT_FILE":         func(c *fileConfig) string { return c.TLSCertFile },
	"TLS_KEY_FILE":          func(c *fileConfig) string { return c.TLSKeyFile },
	"BLOCK_SIGNING_KEY":     func(c *fileConfig) string { return c.BlockSigningKey },
	"WEBHOOK_URL":           func(c *fileConfig) string { return c.WebhookURL },
	"WEBHOOK_SECRET":        func(c *fileConfig) string { return c.WebhookSecret },
	"WEBHOOK_TIMEOUT":       func(c *fileConfig) string { return c.WebhookTimeout },
	"CORS_ALLOWED_ORIGINS":  func(c *fileConfig) string { return c.CORSAllowedOrigins },
	"CORS_ALLOWED_METHODS":  func(c *fileConfig) string { return c.CORSAllowedMethods },
	"CORS_ALLOWED_HEADERS":  func(c *fileConfig) string { return c.CORSAllowedHeaders },
	"CORS_MAX_AGE":          func(c *fileConfig) string { return c.CORSMaxAge },
	"ACCESS_LOG":            func(c *fileConfig) string { return c.AccessLog },
	"AGENT_TTL_SECONDS":     func(c *fileConfig) string { return c.AgentTTLSeconds },
	"MESH_ENABLE":           func(c *fileConfig) string { return c.MeshEnable },
	"MESH_TENANT":           func(c *fileConfig) string { return c.MeshTenant },
	"MESH_GRPC_ADDR":        func(c *fileConfig) string { return c.MeshGRPCAddr },
	"ADMIN_ALLOW_REMOTE":    func(c *fileConfig) string { return c.AdminAllowRemote },
	"ADMIN_ALLOWLIST_CIDRS": func(c *fileConfig) string { return c.AdminAllowlistCIDRs },
	"PUBLIC_PATHS":          func(c *fileConfig) string { return c.PublicPaths },
	"ADMIN_PATHS":           func(c *fileConfig) string { return c.AdminPaths },
	"COMPACT_ON_START":      func(c *fileConfig) string { return c.CompactOnStart },
}

// loadFileConfig attempts to load a JSON config file and apply any values it
// contains as env-var defaults. Values already set in the environment take
// precedence (the file is a fallback, not an override).
//
// The path is resolved from:
//  1. $SPECTRAL_CONFIG env var (explicit override)
//  2. spectral.json in the current directory (convention)
//
// A missing file is silently ignored; parse errors are logged.
func loadFileConfig() {
	path := strings.TrimSpace(os.Getenv("SPECTRAL_CONFIG"))
	if path == "" {
		if _, err := os.Stat("spectral.json"); err == nil {
			path = "spectral.json"
		}
	}
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("config file %s: read error: %v", path, err)
		return
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("config file %s: parse error: %v", path, err)
		return
	}
	applied := 0
	for envKey, getter := range fileConfigEnvMap {
		val := strings.TrimSpace(getter(&cfg))
		if val == "" {
			continue
		}
		// Only set if not already defined in the environment.
		if existing := strings.TrimSpace(os.Getenv(envKey)); existing == "" {
			if err := os.Setenv(envKey, val); err == nil {
				applied++
			}
		}
	}
	if applied > 0 {
		log.Printf("config file %s: applied %d setting(s)", path, applied)
	}
}
