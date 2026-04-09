package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
)

// ---------------------------------------------------------------------------
// getEnvInt / getEnvFloat / getEnvBool / getEnvUint32
// ---------------------------------------------------------------------------

func TestGetEnvInt(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if v := getEnvInt("TEST_INT", 0); v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
	if v := getEnvInt("TEST_INT_MISSING", 7); v != 7 {
		t.Fatalf("expected default 7, got %d", v)
	}
	t.Setenv("TEST_INT_BAD", "notanumber")
	if v := getEnvInt("TEST_INT_BAD", 99); v != 99 {
		t.Fatalf("expected default 99 on bad value, got %d", v)
	}
}

func TestGetEnvFloat(t *testing.T) {
	t.Setenv("TEST_FLOAT", "3.14")
	if v := getEnvFloat("TEST_FLOAT", 0); v != 3.14 {
		t.Fatalf("expected 3.14, got %f", v)
	}
	if v := getEnvFloat("TEST_FLOAT_MISSING", 2.0); v != 2.0 {
		t.Fatalf("expected default 2.0, got %f", v)
	}
	t.Setenv("TEST_FLOAT_BAD", "xyz")
	if v := getEnvFloat("TEST_FLOAT_BAD", 1.5); v != 1.5 {
		t.Fatalf("expected default 1.5, got %f", v)
	}
}

func TestGetEnvBool(t *testing.T) {
	for _, val := range []string{"1", "true", "TRUE", "yes", "YES", "y", "Y"} {
		t.Setenv("TEST_BOOL", val)
		if !getEnvBool("TEST_BOOL", false) {
			t.Fatalf("expected true for %q", val)
		}
	}
	for _, val := range []string{"0", "false", "FALSE", "no", "NO", "n", "N"} {
		t.Setenv("TEST_BOOL", val)
		if getEnvBool("TEST_BOOL", true) {
			t.Fatalf("expected false for %q", val)
		}
	}
	if !getEnvBool("TEST_BOOL_MISSING", true) {
		t.Fatal("expected default true for missing key")
	}
	t.Setenv("TEST_BOOL_BAD", "maybe")
	if getEnvBool("TEST_BOOL_BAD", true) != true {
		t.Fatal("expected default for unrecognised value")
	}
}

func TestGetEnvUint32(t *testing.T) {
	t.Setenv("TEST_U32", "12345")
	if v := getEnvUint32("TEST_U32", 0); v != 12345 {
		t.Fatalf("expected 12345, got %d", v)
	}
	if v := getEnvUint32("TEST_U32_MISSING", 9); v != 9 {
		t.Fatalf("expected default 9, got %d", v)
	}
	t.Setenv("TEST_U32_BAD", "bad")
	if v := getEnvUint32("TEST_U32_BAD", 3); v != 3 {
		t.Fatalf("expected default 3, got %d", v)
	}
}

// ---------------------------------------------------------------------------
// parseKeyList
// ---------------------------------------------------------------------------

func TestParseKeyList_Empty(t *testing.T) {
	if parseKeyList("") != nil {
		t.Fatal("expected nil for empty input")
	}
}

func TestParseKeyList_Valid(t *testing.T) {
	out := parseKeyList("key1, key2 , key3")
	if len(out) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(out))
	}
	if out[0] != "key1" || out[1] != "key2" || out[2] != "key3" {
		t.Fatalf("unexpected keys: %v", out)
	}
}

func TestParseKeyList_SkipsEmpty(t *testing.T) {
	out := parseKeyList("a,,b, ,c")
	if len(out) != 3 {
		t.Fatalf("expected 3 non-empty keys, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// parsePeerKeys
// ---------------------------------------------------------------------------

func TestParsePeerKeys_Empty(t *testing.T) {
	out, err := parsePeerKeys("")
	if err != nil || len(out) != 0 {
		t.Fatalf("expected empty map, got %v %v", out, err)
	}
}

func TestParsePeerKeys_Valid(t *testing.T) {
	out, err := parsePeerKeys("peer1=key1, peer2=key2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["peer1"] != "key1" || out["peer2"] != "key2" {
		t.Fatalf("unexpected map: %v", out)
	}
}

func TestParsePeerKeys_InvalidFormat(t *testing.T) {
	if _, err := parsePeerKeys("noequalssign"); err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestParsePeerKeys_EmptyPeer(t *testing.T) {
	if _, err := parsePeerKeys("=key"); err == nil {
		t.Fatal("expected error for empty peer name")
	}
}

// ---------------------------------------------------------------------------
// parseTags
// ---------------------------------------------------------------------------

func TestParseTags_Empty(t *testing.T) {
	if parseTags(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

func TestParseTags_Valid(t *testing.T) {
	out := parseTags([]string{"env:prod", "region:us-east"})
	if out["env"] != "prod" || out["region"] != "us-east" {
		t.Fatalf("unexpected tags: %v", out)
	}
}

func TestParseTags_SkipsInvalid(t *testing.T) {
	out := parseTags([]string{"valid:yes", "nocolon", ":emptykey"})
	if out["valid"] != "yes" {
		t.Fatalf("expected valid tag, got %v", out)
	}
	if _, ok := out["nocolon"]; ok {
		t.Fatal("nocolon should be skipped")
	}
	if _, ok := out[""]; ok {
		t.Fatal("empty key should be skipped")
	}
}

func TestParseTags_AllInvalid(t *testing.T) {
	if parseTags([]string{"nocolon", ":emptykey"}) != nil {
		t.Fatal("expected nil when all entries are invalid")
	}
}

// ---------------------------------------------------------------------------
// validateTransactions
// ---------------------------------------------------------------------------

func TestValidateTransactions_Valid(t *testing.T) {
	txs := []blockchain.Transaction{
		{Sender: "alice", Recipient: "bob", Amount: 10},
	}
	if err := validateTransactions(txs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTransactions_EmptySender(t *testing.T) {
	txs := []blockchain.Transaction{{Sender: "", Recipient: "bob", Amount: 1}}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for empty sender")
	}
}

func TestValidateTransactions_EmptyRecipient(t *testing.T) {
	txs := []blockchain.Transaction{{Sender: "alice", Recipient: "", Amount: 1}}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for empty recipient")
	}
}

func TestValidateTransactions_NegativeAmount(t *testing.T) {
	txs := []blockchain.Transaction{{Sender: "alice", Recipient: "bob", Amount: -1}}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestValidateTransactions_TooMany(t *testing.T) {
	txs := make([]blockchain.Transaction, 1001)
	for i := range txs {
		txs[i] = blockchain.Transaction{Sender: "a", Recipient: "b", Amount: 1}
	}
	if err := validateTransactions(txs); err == nil {
		t.Fatal("expected error for >1000 transactions")
	}
}

// ---------------------------------------------------------------------------
// parseCIDRList
// ---------------------------------------------------------------------------

func TestParseCIDRList_Empty(t *testing.T) {
	out, err := parseCIDRList("")
	if err != nil || out != nil {
		t.Fatalf("expected nil, nil; got %v, %v", out, err)
	}
}

func TestParseCIDRList_Valid(t *testing.T) {
	out, err := parseCIDRList("10.0.0.0/8, 192.168.1.0/24")
	if err != nil || len(out) != 2 {
		t.Fatalf("expected 2 CIDRs, got %v %v", out, err)
	}
}

func TestParseCIDRList_Invalid(t *testing.T) {
	if _, err := parseCIDRList("notacidr"); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

// ---------------------------------------------------------------------------
// stringInSlice
// ---------------------------------------------------------------------------

func TestStringInSlice(t *testing.T) {
	list := []string{"a", "b", "c"}
	if !stringInSlice("b", list) {
		t.Fatal("expected true for existing element")
	}
	if stringInSlice("z", list) {
		t.Fatal("expected false for missing element")
	}
	if stringInSlice("a", nil) {
		t.Fatal("expected false for nil list")
	}
}

// ---------------------------------------------------------------------------
// isHTTPMethod / isWriteMethod
// ---------------------------------------------------------------------------

func TestIsHTTPMethod(t *testing.T) {
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "get", "post"} {
		if !isHTTPMethod(m) {
			t.Fatalf("expected true for %q", m)
		}
	}
	for _, m := range []string{"CONNECT", "TRACE", "BREW", ""} {
		if isHTTPMethod(m) {
			t.Fatalf("expected false for %q", m)
		}
	}
}

func TestIsWriteMethod(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE", "post"} {
		if !isWriteMethod(m) {
			t.Fatalf("expected true for %q", m)
		}
	}
	for _, m := range []string{"GET", "HEAD", "OPTIONS", ""} {
		if isWriteMethod(m) {
			t.Fatalf("expected false for %q", m)
		}
	}
}

// ---------------------------------------------------------------------------
// secureEquals (via hasValidAPIKey)
// ---------------------------------------------------------------------------

func TestHasValidAPIKey_Bearer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer secret-key")
	if !hasValidAPIKey(r, "secret-key") {
		t.Fatal("expected true for correct bearer token")
	}
}

func TestHasValidAPIKey_XAPIKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", "secret-key")
	if !hasValidAPIKey(r, "secret-key") {
		t.Fatal("expected true for correct X-API-Key")
	}
}

func TestHasValidAPIKey_Wrong(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer wrong-key")
	if hasValidAPIKey(r, "secret-key") {
		t.Fatal("expected false for wrong key")
	}
}

func TestHasValidAPIKey_Missing(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if hasValidAPIKey(r, "secret-key") {
		t.Fatal("expected false with no key")
	}
}

// ---------------------------------------------------------------------------
// isLocalRequest
// ---------------------------------------------------------------------------

func TestIsLocalRequest_Loopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	if !isLocalRequest(r) {
		t.Fatal("expected true for loopback address")
	}
}

func TestIsLocalRequest_IPv6Loopback(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:12345"
	if !isLocalRequest(r) {
		t.Fatal("expected true for ::1")
	}
}

func TestIsLocalRequest_Remote(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:9000"
	if isLocalRequest(r) {
		t.Fatal("expected false for non-loopback address")
	}
}

// ---------------------------------------------------------------------------
// parsePathRule / parsePathRules / matchPathRules
// ---------------------------------------------------------------------------

func TestParsePathRule_SimplePath(t *testing.T) {
	rule, ok := parsePathRule("/health")
	if !ok || rule.value != "/health" || rule.prefix || rule.method != "" {
		t.Fatalf("unexpected rule: %+v %v", rule, ok)
	}
}

func TestParsePathRule_WildcardPrefix(t *testing.T) {
	rule, ok := parsePathRule("/api/*")
	if !ok || rule.value != "/api/" || !rule.prefix {
		t.Fatalf("expected prefix rule, got %+v", rule)
	}
}

func TestParsePathRule_MethodSpace(t *testing.T) {
	rule, ok := parsePathRule("GET /health")
	if !ok || rule.method != "GET" || rule.value != "/health" {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestParsePathRule_MethodColon(t *testing.T) {
	rule, ok := parsePathRule("POST:/submit")
	if !ok || rule.method != "POST" || rule.value != "/submit" {
		t.Fatalf("unexpected rule: %+v", rule)
	}
}

func TestParsePathRule_Empty(t *testing.T) {
	if _, ok := parsePathRule(""); ok {
		t.Fatal("expected false for empty input")
	}
}

func TestParsePathRules_UsesDefaults(t *testing.T) {
	rules := parsePathRules("", []string{"/health", "/metrics"})
	if len(rules) != 2 {
		t.Fatalf("expected 2 default rules, got %d", len(rules))
	}
}

func TestParsePathRules_Override(t *testing.T) {
	rules := parsePathRules("/api/*,/health", nil)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestMatchPathRules_Exact(t *testing.T) {
	rules := parsePathRules("/health", nil)
	if !matchPathRules("/health", "GET", rules) {
		t.Fatal("expected match for exact path")
	}
	if matchPathRules("/other", "GET", rules) {
		t.Fatal("expected no match for different path")
	}
}

func TestMatchPathRules_Prefix(t *testing.T) {
	rules := parsePathRules("/api/*", nil)
	if !matchPathRules("/api/v1/agents", "GET", rules) {
		t.Fatal("expected prefix match")
	}
	if matchPathRules("/other/path", "GET", rules) {
		t.Fatal("expected no match outside prefix")
	}
}

func TestMatchPathRules_MethodFilter(t *testing.T) {
	rules := parsePathRules("GET:/health", nil)
	if !matchPathRules("/health", "GET", rules) {
		t.Fatal("expected match for GET /health")
	}
	if matchPathRules("/health", "POST", rules) {
		t.Fatal("expected no match for POST /health")
	}
}

// ---------------------------------------------------------------------------
// adminIPAllowed
// ---------------------------------------------------------------------------

func TestAdminIPAllowed_Loopback_NoList(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9000"
	if !adminIPAllowed(r, nil, false) {
		t.Fatal("expected loopback allowed with no allowlist")
	}
}

func TestAdminIPAllowed_Remote_AllowRemote(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:9000"
	if !adminIPAllowed(r, nil, true) {
		t.Fatal("expected remote allowed when allowRemote=true")
	}
}

func TestAdminIPAllowed_Remote_DenyRemote(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:9000"
	if adminIPAllowed(r, nil, false) {
		t.Fatal("expected remote denied when allowRemote=false")
	}
}

func TestAdminIPAllowed_CIDR_Match(t *testing.T) {
	cidrs, _ := parseCIDRList("10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.5.6.7:9000"
	if !adminIPAllowed(r, cidrs, false) {
		t.Fatal("expected match within CIDR")
	}
}

func TestAdminIPAllowed_CIDR_NoMatch(t *testing.T) {
	cidrs, _ := parseCIDRList("10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:9000"
	if adminIPAllowed(r, cidrs, false) {
		t.Fatal("expected deny outside CIDR")
	}
}

// ---------------------------------------------------------------------------
// detectAnomaly
// ---------------------------------------------------------------------------

func TestDetectAnomaly_TooFewSamples(t *testing.T) {
	ok, reason := detectAnomaly(0.9, 10, 5, 0.5, 100, 10, nil)
	if ok || reason != "" {
		t.Fatal("expected no anomaly when below minSamples")
	}
}

func TestDetectAnomaly_BurstThreshold(t *testing.T) {
	ok, reason := detectAnomaly(0.1, 50, 100, 0.9, 10, 5, nil)
	if !ok || reason != "reject_burst" {
		t.Fatalf("expected reject_burst, got %v %q", ok, reason)
	}
}

func TestDetectAnomaly_RateThreshold(t *testing.T) {
	ok, reason := detectAnomaly(0.8, 5, 100, 0.5, 100, 5, nil)
	if !ok || reason != "reject_rate" {
		t.Fatalf("expected reject_rate, got %v %q", ok, reason)
	}
}

func TestDetectAnomaly_RateTrend(t *testing.T) {
	window := []float64{0.1, 0.2, 0.3}
	ok, reason := detectAnomaly(0.3, 0, 100, 0.5, 0, 5, window)
	if !ok || reason != "reject_rate_trend" {
		t.Fatalf("expected reject_rate_trend, got %v %q", ok, reason)
	}
}

func TestDetectAnomaly_NoAnomaly(t *testing.T) {
	ok, reason := detectAnomaly(0.01, 1, 100, 0.9, 100, 5, nil)
	if ok || reason != "" {
		t.Fatalf("expected no anomaly, got %v %q", ok, reason)
	}
}

// ---------------------------------------------------------------------------
// filterValidBlocks (server-side)
// ---------------------------------------------------------------------------

func TestFilterValidBlocksServer_AllValid(t *testing.T) {
	bc := blockchain.NewBlockchain()
	bc.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	bc.AddBlock([]blockchain.Transaction{{Sender: "b", Recipient: "c", Amount: 2}})
	blocks := bc.Snapshot()
	out := filterValidBlocks(blocks)
	if len(out) != len(blocks) {
		t.Fatalf("expected %d, got %d", len(blocks), len(out))
	}
}

func TestFilterValidBlocksServer_StopsAtBad(t *testing.T) {
	bc := blockchain.NewBlockchain()
	bc.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := bc.Snapshot()
	blocks[1].Hash = "tampered"
	out := filterValidBlocks(blocks)
	if len(out) != 1 {
		t.Fatalf("expected 1 valid block, got %d", len(out))
	}
}

func TestFilterValidBlocksServer_Nil(t *testing.T) {
	if filterValidBlocks(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

// ---------------------------------------------------------------------------
// pruneExpiredRoutes
// ---------------------------------------------------------------------------

func TestPruneExpiredRoutesServer_KeepsLive(t *testing.T) {
	routes := []routing.Route{{Destination: "alive"}}
	out := pruneExpiredRoutes(routes)
	if len(out) != 1 {
		t.Fatalf("expected 1 route, got %d", len(out))
	}
}

func TestPruneExpiredRoutesServer_RemovesExpired(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	routes := []routing.Route{
		{Destination: "dead", ExpiresAt: &past},
		{Destination: "live", ExpiresAt: &future},
	}
	out := pruneExpiredRoutes(routes)
	if len(out) != 1 || out[0].Destination != "live" {
		t.Fatalf("expected only 'live', got %v", out)
	}
}

// ---------------------------------------------------------------------------
// backupFile / rotateBackups
// ---------------------------------------------------------------------------

func TestBackupFile_NonExistent(t *testing.T) {
	ok, err := backupFile(filepath.Join(t.TempDir(), "nofile.db"))
	if err != nil || ok {
		t.Fatalf("expected false,nil for non-existent file; got %v,%v", ok, err)
	}
}

func TestBackupFile_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := backupFile(path)
	if err != nil || !ok {
		t.Fatalf("expected true,nil; got %v,%v", ok, err)
	}
	matches, _ := filepath.Glob(path + ".*.bak")
	if len(matches) != 1 {
		t.Fatalf("expected 1 backup file, got %d", len(matches))
	}
}

func TestBackupFile_Directory(t *testing.T) {
	dir := t.TempDir()
	ok, err := backupFile(dir)
	if err != nil || ok {
		t.Fatalf("expected false,nil for directory; got %v,%v", ok, err)
	}
}

func TestRotateBackups_ZeroRetain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	// Create two backup files.
	for _, n := range []string{"data.db.20240101T000000Z.bak", "data.db.20240102T000000Z.bak"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	if err := rotateBackups(path, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// retain=0 means skip rotation — files should still be there.
	matches, _ := filepath.Glob(path + ".*.bak")
	if len(matches) != 2 {
		t.Fatalf("expected 2 files with retain=0, got %d", len(matches))
	}
}

func TestRotateBackups_Prunes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.db")
	names := []string{
		"data.db.20240101T000000Z.bak",
		"data.db.20240102T000000Z.bak",
		"data.db.20240103T000000Z.bak",
		"data.db.20240104T000000Z.bak",
	}
	for _, n := range names {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	if err := rotateBackups(path, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	matches, _ := filepath.Glob(path + ".*.bak")
	if len(matches) != 2 {
		t.Fatalf("expected 2 files after rotation, got %d", len(matches))
	}
	sort.Strings(matches)
	// Only the two newest should remain.
	if filepath.Base(matches[0]) != "data.db.20240103T000000Z.bak" {
		t.Fatalf("unexpected oldest remaining: %s", filepath.Base(matches[0]))
	}
}

// ---------------------------------------------------------------------------
// statusTracker
// ---------------------------------------------------------------------------

func TestStatusTracker_Snapshot_Initial(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC()}
	snap := s.snapshot()
	if snap.LastBackup != nil || snap.LastCompact != nil {
		t.Fatal("expected nil backup/compact on fresh tracker")
	}
	if snap.LastBackupError != nil || snap.LastCompactError != nil {
		t.Fatal("expected nil error fields on fresh tracker")
	}
}

func TestStatusTracker_RecordBackup_Success(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC()}
	s.recordBackup(nil)
	snap := s.snapshot()
	if snap.LastBackup == nil {
		t.Fatal("expected LastBackup to be set after recordBackup")
	}
	if snap.LastBackupError != nil {
		t.Fatal("expected no error after successful backup")
	}
}

func TestStatusTracker_RecordBackup_Error(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC()}
	s.recordBackup(errors.New("disk full"))
	snap := s.snapshot()
	if snap.LastBackupError == nil || *snap.LastBackupError != "disk full" {
		t.Fatalf("expected error 'disk full', got %v", snap.LastBackupError)
	}
}

func TestStatusTracker_RecordCompact_Success(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC()}
	s.recordCompact(nil)
	snap := s.snapshot()
	if snap.LastCompact == nil {
		t.Fatal("expected LastCompact to be set")
	}
}

func TestStatusTracker_RecordCompact_Error(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC()}
	s.recordCompact(errors.New("compaction failed"))
	snap := s.snapshot()
	if snap.LastCompactError == nil || *snap.LastCompactError != "compaction failed" {
		t.Fatalf("expected compaction error, got %v", snap.LastCompactError)
	}
}

func TestStatusTracker_UptimeIncreases(t *testing.T) {
	s := &statusTracker{startedAt: time.Now().UTC().Add(-5 * time.Second)}
	snap := s.snapshot()
	if snap.UptimeSeconds < 5 {
		t.Fatalf("expected uptime >= 5s, got %d", snap.UptimeSeconds)
	}
}

// ---------------------------------------------------------------------------
// ipLimiter / tenantLimiter
// ---------------------------------------------------------------------------

func TestIPLimiter_AllowsRequests(t *testing.T) {
	lim := newIPLimiter(100, 10, time.Minute)
	limiter := lim.get("10.0.0.1")
	if !limiter.Allow() {
		t.Fatal("expected request to be allowed")
	}
}

func TestIPLimiter_SameIPSameLimiter(t *testing.T) {
	lim := newIPLimiter(100, 10, time.Minute)
	a := lim.get("10.0.0.2")
	b := lim.get("10.0.0.2")
	if a != b {
		t.Fatal("expected same limiter for same IP")
	}
}

func TestIPLimiter_DifferentIPDifferentLimiter(t *testing.T) {
	lim := newIPLimiter(100, 10, time.Minute)
	a := lim.get("10.0.0.1")
	b := lim.get("10.0.0.2")
	if a == b {
		t.Fatal("expected different limiters for different IPs")
	}
}

func TestTenantLimiter_EmptyTenantReturnsNil(t *testing.T) {
	lim := newTenantLimiter(100, 10, time.Minute)
	if lim.get("") != nil {
		t.Fatal("expected nil for empty tenant")
	}
}

func TestTenantLimiter_AllowsRequests(t *testing.T) {
	lim := newTenantLimiter(100, 10, time.Minute)
	l := lim.get("tenant-a")
	if l == nil || !l.Allow() {
		t.Fatal("expected limiter to allow requests")
	}
}

func TestTenantLimiter_SameTenantSameLimiter(t *testing.T) {
	lim := newTenantLimiter(100, 10, time.Minute)
	a := lim.get("tenant-x")
	b := lim.get("tenant-x")
	if a != b {
		t.Fatal("expected same limiter for same tenant")
	}
}

func TestTenantLimiter_Cleanup(t *testing.T) {
	lim := newTenantLimiter(100, 10, 1*time.Millisecond)
	_ = lim.get("tenant-old")
	time.Sleep(10 * time.Millisecond)
	lim.cleanup(time.Now())
	lim.mu.Lock()
	n := len(lim.entries)
	lim.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// withRateLimit middleware
// ---------------------------------------------------------------------------

func TestWithRateLimit_NilLimiter(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withRateLimit(inner, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("expected handler to be called when limiter is nil")
	}
}

func TestWithRateLimit_AllowsNormal(t *testing.T) {
	lim := newIPLimiter(100, 100, time.Minute)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withRateLimit(inner, lim)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9999"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("expected request to be allowed")
	}
}

func TestWithRateLimit_BlocksWhenExceeded(t *testing.T) {
	lim := newIPLimiter(0, 0, time.Minute) // zero rate + burst → always blocked
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := withRateLimit(inner, lim)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:9999"
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// withTenantRateLimit middleware
// ---------------------------------------------------------------------------

func TestWithTenantRateLimit_AllowsNormal(t *testing.T) {
	lim := newTenantLimiter(100, 100, time.Minute)
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withTenantRateLimit(inner, lim, "default")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("expected request to be allowed")
	}
}

func TestWithTenantRateLimit_BlocksWhenExceeded(t *testing.T) {
	lim := newTenantLimiter(0, 0, time.Minute)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := withTenantRateLimit(inner, lim, "default")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}
