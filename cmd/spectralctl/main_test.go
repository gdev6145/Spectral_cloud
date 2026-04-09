package main

import (
	"encoding/json"
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
// parseBuckets
// ---------------------------------------------------------------------------

func TestParseBuckets_Empty(t *testing.T) {
	out, err := parseBuckets("")
	if err != nil || out != nil {
		t.Fatalf("expected nil, nil; got %v, %v", out, err)
	}
}

func TestParseBuckets_Valid(t *testing.T) {
	out, err := parseBuckets("100,50,200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(out))
	}
	// Should be sorted ascending.
	if !sort.SliceIsSorted(out, func(i, j int) bool { return out[i] < out[j] }) {
		t.Fatal("buckets should be sorted ascending")
	}
	if out[0] != 50 || out[1] != 100 || out[2] != 200 {
		t.Fatalf("unexpected bucket values: %v", out)
	}
}

func TestParseBuckets_NonNumeric(t *testing.T) {
	if _, err := parseBuckets("10,abc,50"); err == nil {
		t.Fatal("expected error for non-numeric bucket")
	}
}

func TestParseBuckets_ZeroValue(t *testing.T) {
	if _, err := parseBuckets("0,10"); err == nil {
		t.Fatal("expected error for zero bucket value")
	}
}

func TestParseBuckets_NegativeValue(t *testing.T) {
	if _, err := parseBuckets("-5,10"); err == nil {
		t.Fatal("expected error for negative bucket")
	}
}

func TestParseBuckets_WithSpaces(t *testing.T) {
	out, err := parseBuckets(" 10 , 20 , 30 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// percentile
// ---------------------------------------------------------------------------

func TestPercentile_Empty(t *testing.T) {
	if percentile(nil, 0.5) != 0 {
		t.Fatal("expected 0 for empty slice")
	}
}

func TestPercentile_Zero(t *testing.T) {
	vals := []int64{1, 2, 3, 4, 5}
	if percentile(vals, 0) != 1 {
		t.Fatalf("p0 should be first element")
	}
}

func TestPercentile_One(t *testing.T) {
	vals := []int64{1, 2, 3, 4, 5}
	if percentile(vals, 1) != 5 {
		t.Fatalf("p100 should be last element")
	}
}

func TestPercentile_50(t *testing.T) {
	vals := []int64{10, 20, 30, 40, 50}
	p := percentile(vals, 0.5)
	if p < 20 || p > 40 {
		t.Fatalf("unexpected p50: %v", p)
	}
}

func TestPercentile_Single(t *testing.T) {
	vals := []int64{42}
	if percentile(vals, 0.5) != 42 {
		t.Fatalf("single-element percentile should return that element")
	}
}

// ---------------------------------------------------------------------------
// summarizeLatencies
// ---------------------------------------------------------------------------

func TestSummarizeLatencies_Empty(t *testing.T) {
	avg, p50, p95, p99, max := summarizeLatencies(nil)
	if avg != 0 || p50 != 0 || p95 != 0 || p99 != 0 || max != 0 {
		t.Fatal("all values should be 0 for empty input")
	}
}

func TestSummarizeLatencies_Single(t *testing.T) {
	lats := []time.Duration{100 * time.Millisecond}
	avg, p50, _, _, max := summarizeLatencies(lats)
	if avg != 100 {
		t.Fatalf("expected avg=100ms, got %.2f", avg)
	}
	if p50 != 100 {
		t.Fatalf("expected p50=100ms, got %.2f", p50)
	}
	if max != 100 {
		t.Fatalf("expected max=100ms, got %.2f", max)
	}
}

func TestSummarizeLatencies_Multiple(t *testing.T) {
	lats := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}
	avg, _, _, _, max := summarizeLatencies(lats)
	if avg != 30 {
		t.Fatalf("expected avg=30ms, got %.2f", avg)
	}
	if max != 50 {
		t.Fatalf("expected max=50ms, got %.2f", max)
	}
}

// ---------------------------------------------------------------------------
// topErrorCounts
// ---------------------------------------------------------------------------

func TestTopErrorCounts_Empty(t *testing.T) {
	if topErrorCounts(nil, 5) != nil {
		t.Fatal("expected nil for empty counts")
	}
}

func TestTopErrorCounts_ZeroLimit(t *testing.T) {
	counts := map[string]int{"err1": 3}
	if topErrorCounts(counts, 0) != nil {
		t.Fatal("expected nil for zero limit")
	}
}

func TestTopErrorCounts_Sorted(t *testing.T) {
	counts := map[string]int{"a": 1, "b": 5, "c": 3}
	out := topErrorCounts(counts, 10)
	if len(out) != 3 {
		t.Fatalf("expected 3 results, got %d", len(out))
	}
	if out[0].Error != "b" || out[0].Count != 5 {
		t.Fatalf("expected b=5 first, got %v", out[0])
	}
}

func TestTopErrorCounts_Limit(t *testing.T) {
	counts := map[string]int{"a": 1, "b": 5, "c": 3, "d": 2}
	out := topErrorCounts(counts, 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 results (limit), got %d", len(out))
	}
	if out[0].Count < out[1].Count {
		t.Fatal("results should be in descending order")
	}
}

// ---------------------------------------------------------------------------
// loadStats
// ---------------------------------------------------------------------------

func TestLoadStats_Empty(t *testing.T) {
	s := newLoadStats(nil)
	s.finish(1 * time.Second)
	sum := s.summary()
	if sum.Total != 0 || sum.Successes != 0 || sum.Failures != 0 {
		t.Fatalf("expected empty summary, got %+v", sum)
	}
}

func TestLoadStats_Record(t *testing.T) {
	s := newLoadStats(nil)
	s.record(true, 10*time.Millisecond)
	s.record(true, 20*time.Millisecond)
	s.record(false, 5*time.Millisecond)
	s.finish(1 * time.Second)

	sum := s.summary()
	if sum.Total != 3 {
		t.Fatalf("expected total=3, got %d", sum.Total)
	}
	if sum.Successes != 2 {
		t.Fatalf("expected successes=2, got %d", sum.Successes)
	}
	if sum.Failures != 1 {
		t.Fatalf("expected failures=1, got %d", sum.Failures)
	}
	if sum.ElapsedSeconds != 1.0 {
		t.Fatalf("expected elapsed=1s, got %.2f", sum.ElapsedSeconds)
	}
	if sum.RPS != 3.0 {
		t.Fatalf("expected rps=3.0, got %.2f", sum.RPS)
	}
}

func TestLoadStats_Histogram(t *testing.T) {
	buckets, _ := parseBuckets("10,50,100")
	s := newLoadStats(buckets)
	s.record(true, 5*time.Millisecond)   // <=10 bucket
	s.record(true, 30*time.Millisecond)  // <=50 bucket
	s.record(true, 80*time.Millisecond)  // <=100 bucket
	s.record(true, 200*time.Millisecond) // >100 bucket
	s.finish(2 * time.Second)

	sum := s.summary()
	if len(sum.Histogram) != 4 {
		t.Fatalf("expected 4 histogram buckets, got %d", len(sum.Histogram))
	}
	// First bucket (<=10ms) should have count=1.
	if sum.Histogram[0].Count != 1 {
		t.Fatalf("expected bucket[0].count=1, got %d", sum.Histogram[0].Count)
	}
	// Overflow bucket (>100ms) should have count=1.
	if sum.Histogram[3].Count != 1 {
		t.Fatalf("expected overflow bucket count=1, got %d", sum.Histogram[3].Count)
	}
}

func TestLoadStats_ZeroElapsed(t *testing.T) {
	s := newLoadStats(nil)
	s.record(true, 10*time.Millisecond)
	s.finish(0) // zero elapsed → RPS should be 0
	sum := s.summary()
	if sum.RPS != 0 {
		t.Fatalf("expected RPS=0 with zero elapsed, got %.2f", sum.RPS)
	}
}

// ---------------------------------------------------------------------------
// validateBlocks
// ---------------------------------------------------------------------------

func TestValidateBlocks_Empty(t *testing.T) {
	issues := validateBlocks(nil)
	if len(issues) != 0 {
		t.Fatalf("expected no issues for empty chain, got %v", issues)
	}
}

func TestValidateBlocks_ValidChain(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	chain.AddBlock([]blockchain.Transaction{{Sender: "b", Recipient: "c", Amount: 2}})
	issues := validateBlocks(chain.Snapshot())
	if len(issues) != 0 {
		t.Fatalf("expected no issues for valid chain, got %v", issues)
	}
}

func TestValidateBlocks_TamperedHash(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "x", Recipient: "y", Amount: 10}})
	blocks := chain.Snapshot()
	// Tamper with the hash.
	blocks[0].Hash = "deadbeef"
	issues := validateBlocks(blocks)
	if len(issues) == 0 {
		t.Fatal("expected issues for tampered block hash")
	}
}

// ---------------------------------------------------------------------------
// validateRouteList
// ---------------------------------------------------------------------------

func TestValidateRouteList_Empty(t *testing.T) {
	if issues := validateRouteList(nil); len(issues) != 0 {
		t.Fatalf("expected no issues for empty list, got %v", issues)
	}
}

func TestValidateRouteList_Valid(t *testing.T) {
	routes := []routing.Route{
		{Destination: "10.0.0.1:9000", Metric: routing.RouteMetric{Latency: 5}},
	}
	if issues := validateRouteList(routes); len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}

func TestValidateRouteList_EmptyDestination(t *testing.T) {
	routes := []routing.Route{{Destination: ""}}
	issues := validateRouteList(routes)
	if len(issues) == 0 {
		t.Fatal("expected issue for empty destination")
	}
}

func TestValidateRouteList_ExpiredRoute(t *testing.T) {
	past := time.Now().UTC().Add(-1 * time.Hour)
	routes := []routing.Route{
		{Destination: "10.0.0.1:9000", ExpiresAt: &past},
	}
	issues := validateRouteList(routes)
	if len(issues) == 0 {
		t.Fatal("expected issue for expired route")
	}
}

func TestValidateRouteList_NotExpired(t *testing.T) {
	future := time.Now().UTC().Add(1 * time.Hour)
	routes := []routing.Route{
		{Destination: "10.0.0.1:9000", ExpiresAt: &future},
	}
	if issues := validateRouteList(routes); len(issues) != 0 {
		t.Fatalf("expected no issues for non-expired route, got %v", issues)
	}
}

// ---------------------------------------------------------------------------
// pruneExpired
// ---------------------------------------------------------------------------

func TestPruneExpired_None(t *testing.T) {
	routes := []routing.Route{
		{Destination: "a"},
		{Destination: "b"},
	}
	out := pruneExpired(routes)
	if len(out) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(out))
	}
}

func TestPruneExpired_AllExpired(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	routes := []routing.Route{
		{Destination: "a", ExpiresAt: &past},
		{Destination: "b", ExpiresAt: &past},
	}
	out := pruneExpired(routes)
	if len(out) != 0 {
		t.Fatalf("expected 0 routes after pruning, got %d", len(out))
	}
}

func TestPruneExpired_Mixed(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	routes := []routing.Route{
		{Destination: "a", ExpiresAt: &past},
		{Destination: "b", ExpiresAt: &future},
		{Destination: "c"},
	}
	out := pruneExpired(routes)
	if len(out) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(out))
	}
	for _, r := range out {
		if r.Destination == "a" {
			t.Fatal("expired route 'a' should have been pruned")
		}
	}
}

// ---------------------------------------------------------------------------
// filterValidBlocks
// ---------------------------------------------------------------------------

func TestFilterValidBlocks_AllValid(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	chain.AddBlock([]blockchain.Transaction{{Sender: "b", Recipient: "c", Amount: 2}})
	blocks := chain.Snapshot()
	out := filterValidBlocks(blocks)
	if len(out) != len(blocks) {
		t.Fatalf("expected %d blocks, got %d", len(blocks), len(out))
	}
}

func TestFilterValidBlocks_StopsAtBadBlock(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	chain.AddBlock([]blockchain.Transaction{{Sender: "b", Recipient: "c", Amount: 2}})
	blocks := chain.Snapshot()
	// Tamper block at index 1.
	blocks[1].Hash = "badhash"
	out := filterValidBlocks(blocks)
	if len(out) != 1 {
		t.Fatalf("expected 1 valid block before tampered block, got %d", len(out))
	}
}

func TestFilterValidBlocks_Empty(t *testing.T) {
	out := filterValidBlocks(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// apiGET
// ---------------------------------------------------------------------------

func TestAPIGET_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, status, err := apiGET(srv.URL, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIGET_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, _, err := apiGET(srv.URL, "my-key", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer my-key" {
		t.Fatalf("expected 'Bearer my-key', got %q", gotAuth)
	}
}

func TestAPIGET_NoAuthHeaderWhenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, _, err := apiGET(srv.URL, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestAPIGET_Non200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	body, status, err := apiGET(srv.URL, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 401 {
		t.Fatalf("expected 401, got %d", status)
	}
	if string(body) != "unauthorized" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestAPIGET_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, _, err := apiGET(srv.URL, "", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAPIGET_InvalidURL(t *testing.T) {
	_, _, err := apiGET("://bad-url", "", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// ---------------------------------------------------------------------------
// windowStats
// ---------------------------------------------------------------------------

func TestWindowStats_DisabledByZeroDuration(t *testing.T) {
w := newWindowStats(0, false, false)
if w.enabled {
t.Fatal("expected disabled for zero window")
}
w.record(true, 10*time.Millisecond, "")
w.finish()
if w.summaries() != nil {
t.Fatal("expected nil summaries when disabled")
}
}

func TestWindowStats_RecordAndFlush(t *testing.T) {
w := newWindowStats(100*time.Millisecond, false, false)
w.record(true, 10*time.Millisecond, "")
w.record(true, 20*time.Millisecond, "")
w.record(false, 5*time.Millisecond, "timeout")
w.flush(time.Now().UTC())
sums := w.summaries()
if len(sums) != 1 {
t.Fatalf("expected 1 window summary, got %d", len(sums))
}
s := sums[0]
if s.Successes != 2 || s.Failures != 1 {
t.Fatalf("unexpected counts: ok=%d fail=%d", s.Successes, s.Failures)
}
}

func TestWindowStats_FlushEmptyNoSummary(t *testing.T) {
w := newWindowStats(100*time.Millisecond, false, false)
w.flush(time.Now().UTC()) // no data → no summary appended
if len(w.summaries()) != 0 {
t.Fatal("expected no summaries for empty flush")
}
}

func TestWindowStats_StopIdempotent(t *testing.T) {
w := newWindowStats(50*time.Millisecond, false, false)
w.start()
w.stop()
w.stop() // double-stop should not panic
}

func TestWindowStats_ErrorCounts(t *testing.T) {
w := newWindowStats(100*time.Millisecond, false, false)
w.record(false, 1*time.Millisecond, "connection refused")
w.record(false, 1*time.Millisecond, "connection refused")
w.record(false, 1*time.Millisecond, "timeout")
w.flush(time.Now().UTC())
sums := w.summaries()
if len(sums) != 1 {
t.Fatalf("expected 1 summary, got %d", len(sums))
}
if len(sums[0].ErrorTop) == 0 {
t.Fatal("expected top errors in summary")
}
if sums[0].ErrorTop[0].Error != "connection refused" || sums[0].ErrorTop[0].Count != 2 {
t.Fatalf("unexpected top error: %+v", sums[0].ErrorTop[0])
}
}

// ---------------------------------------------------------------------------
// writeJSON / readBlocksFromDB / writeBlocksToDB / readRoutesFromDB / writeRoutesToDB
// ---------------------------------------------------------------------------

func TestWriteJSON_RoundTrip(t *testing.T) {
dir := t.TempDir()
path := filepath.Join(dir, "data.json")
in := map[string]int{"a": 1, "b": 2}
if err := writeJSON(path, in); err != nil {
t.Fatalf("writeJSON: %v", err)
}
raw, err := os.ReadFile(path)
if err != nil {
t.Fatal(err)
}
var out map[string]int
if err := json.Unmarshal(raw, &out); err != nil {
t.Fatal(err)
}
if out["a"] != 1 || out["b"] != 2 {
t.Fatalf("unexpected round-trip: %v", out)
}
}

func TestReadWriteBlocksFromDB(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

// Non-existent DB → returns false, no error.
if _, ok, err := readBlocksFromDB(dbPath); err != nil || ok {
t.Fatalf("expected false,nil for missing DB; got ok=%v err=%v", ok, err)
}

// Write blocks.
bc := blockchain.NewBlockchain()
bc.AddBlock([]blockchain.Transaction{{Sender: "x", Recipient: "y", Amount: 5}})
if err := writeBlocksToDB(dbPath, bc.Snapshot()); err != nil {
t.Fatalf("writeBlocksToDB: %v", err)
}

// Read back.
blocks, ok, err := readBlocksFromDB(dbPath)
if err != nil || !ok || len(blocks) != 2 {
t.Fatalf("expected 2 blocks; got ok=%v err=%v len=%d", ok, err, len(blocks))
}
}

func TestReadWriteRoutesFromDB(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

// Non-existent → false, no error.
if _, ok, err := readRoutesFromDB(dbPath); err != nil || ok {
t.Fatalf("expected false,nil; got ok=%v err=%v", ok, err)
}

routes := []routing.Route{{Destination: "node-a", Metric: routing.RouteMetric{Latency: 10}}}
if err := writeRoutesToDB(dbPath, routes); err != nil {
t.Fatalf("writeRoutesToDB: %v", err)
}

got, ok, err := readRoutesFromDB(dbPath)
if err != nil || !ok || len(got) != 1 {
t.Fatalf("expected 1 route; got ok=%v err=%v len=%d", ok, err, len(got))
}
if got[0].Destination != "node-a" {
t.Fatalf("unexpected destination: %s", got[0].Destination)
}
}

// ---------------------------------------------------------------------------
// repairBlockchain / repairRoutes
// ---------------------------------------------------------------------------

func TestRepairBlockchain_AllValid(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

bc := blockchain.NewBlockchain()
bc.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
_ = writeBlocksToDB(dbPath, bc.Snapshot())

if err := repairBlockchain("", dbPath); err != nil {
t.Fatalf("repairBlockchain: %v", err)
}

// All blocks were valid → all preserved.
blocks, _, _ := readBlocksFromDB(dbPath)
if len(blocks) != 2 {
t.Fatalf("expected 2 blocks, got %d", len(blocks))
}
}

func TestRepairBlockchain_TamperedBlock(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

bc := blockchain.NewBlockchain()
bc.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
bc.AddBlock([]blockchain.Transaction{{Sender: "b", Recipient: "c", Amount: 2}})
snap := bc.Snapshot()
// Tamper block index 2.
snap[2].Hash = "badhash"
_ = writeBlocksToDB(dbPath, snap)

if err := repairBlockchain("", dbPath); err != nil {
t.Fatalf("repairBlockchain: %v", err)
}

blocks, _, _ := readBlocksFromDB(dbPath)
if len(blocks) != 2 {
t.Fatalf("expected 2 valid blocks after repair, got %d", len(blocks))
}
}

func TestRepairBlockchain_NonExistentFiles(t *testing.T) {
// Both DB and JSON missing → should return nil (no error).
if err := repairBlockchain("/tmp/no-such-file.json", "/tmp/no-such-db.db"); err != nil {
t.Fatalf("expected nil for missing files, got %v", err)
}
}

func TestRepairRoutes_PrunesExpired(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

past := time.Now().UTC().Add(-time.Hour)
future := time.Now().UTC().Add(time.Hour)
routes := []routing.Route{
{Destination: "dead", Metric: routing.RouteMetric{Latency: 1}, ExpiresAt: &past},
{Destination: "live", Metric: routing.RouteMetric{Latency: 2}, ExpiresAt: &future},
}
_ = writeRoutesToDB(dbPath, routes)

if err := repairRoutes("", dbPath); err != nil {
t.Fatalf("repairRoutes: %v", err)
}

got, _, _ := readRoutesFromDB(dbPath)
if len(got) != 1 || got[0].Destination != "live" {
t.Fatalf("expected only 'live' route after repair, got %v", got)
}
}

func TestRepairRoutes_NonExistentFiles(t *testing.T) {
if err := repairRoutes("/tmp/no-routes.json", "/tmp/no-db.db"); err != nil {
t.Fatalf("expected nil for missing files, got %v", err)
}
}

// ---------------------------------------------------------------------------
// validateBlockchain / validateRoutes (via DB)
// ---------------------------------------------------------------------------

func TestValidateBlockchainDB_Valid(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

bc := blockchain.NewBlockchain()
bc.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
_ = writeBlocksToDB(dbPath, bc.Snapshot())

issues, err := validateBlockchain("", dbPath)
if err != nil {
t.Fatalf("validateBlockchain: %v", err)
}
// May have schema issues if schema file absent; check no crash.
_ = issues
}

func TestValidateBlockchainDB_MissingFile(t *testing.T) {
issues, err := validateBlockchain("/tmp/nofile.json", "/tmp/nodb.db")
if err != nil {
t.Fatalf("expected nil error for missing, got %v", err)
}
if issues != nil {
t.Fatalf("expected nil issues for missing, got %v", issues)
}
}

func TestValidateRoutesDB_Valid(t *testing.T) {
dir := t.TempDir()
dbPath := filepath.Join(dir, "test.db")

routes := []routing.Route{{Destination: "node-a", Metric: routing.RouteMetric{Latency: 5}}}
_ = writeRoutesToDB(dbPath, routes)

_, err := validateRoutes("", dbPath)
if err != nil {
t.Fatalf("validateRoutes: %v", err)
}
// Schema issues may appear when no schema file is provided; just verify no crash.
}

func TestValidateRoutesDB_MissingFile(t *testing.T) {
issues, err := validateRoutes("/tmp/nofile.json", "/tmp/nodb.db")
if err != nil {
t.Fatalf("expected nil for missing files, got %v", err)
}
if issues != nil {
t.Fatalf("expected nil issues, got %v", issues)
}
}
