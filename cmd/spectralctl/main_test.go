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

	"strings"

	"github.com/gdev6145/Spectral_cloud/pkg/blockchain"
	"github.com/gdev6145/Spectral_cloud/pkg/routing"
	"github.com/gdev6145/Spectral_cloud/pkg/store"
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

// ---------------------------------------------------------------------------
// apiPOST
// ---------------------------------------------------------------------------

func TestApiPOST_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when apiKey is empty")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, status, err := apiPOST(srv.URL, "", []byte(`{}`), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestApiPOST_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mytoken" {
			t.Errorf("expected Authorization: Bearer mytoken, got %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, status, err := apiPOST(srv.URL, "mytoken", []byte(`{}`), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("expected 201, got %d", status)
	}
}

func TestApiPOST_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	body, status, err := apiPOST(srv.URL, "", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", status)
	}
	if len(body) == 0 {
		t.Fatal("expected non-empty body")
	}
}

// ---------------------------------------------------------------------------
// apiPUT
// ---------------------------------------------------------------------------

func TestApiPUT_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"updated":true}`))
	}))
	defer srv.Close()

	body, status, err := apiPUT(srv.URL, "", []byte(`{"x":1}`), 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if string(body) != `{"updated":true}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestApiPUT_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer putkey" {
			t.Errorf("expected Bearer putkey, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, status, err := apiPUT(srv.URL, "putkey", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
}

func TestApiPUT_NoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when apiKey is empty")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, status, err := apiPUT(srv.URL, "", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// apiDELETE
// ---------------------------------------------------------------------------

func TestApiDELETE_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, status, err := apiDELETE(srv.URL, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}
}

func TestApiDELETE_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer delkey" {
			t.Errorf("expected Bearer delkey, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deleted":true}`))
	}))
	defer srv.Close()

	body, status, err := apiDELETE(srv.URL, "delkey", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if string(body) != `{"deleted":true}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestApiDELETE_NoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no Authorization header when apiKey is empty")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, status, err := apiDELETE(srv.URL, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// printIssues
// ---------------------------------------------------------------------------

func TestPrintIssues_NoIssues(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printIssues("blockchain", nil)

	_ = w.Close()
	os.Stdout = old
	buf := make([]byte, 128)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if out != "blockchain: ok\n" {
		t.Fatalf("expected 'blockchain: ok\\n', got %q", out)
	}
}

func TestPrintIssues_WithIssues(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printIssues("routes", []string{"route X expired", "empty destination"})

	_ = w.Close()
	os.Stdout = old
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if out == "" {
		t.Fatal("expected non-empty output from printIssues with issues")
	}
}

// ---------------------------------------------------------------------------
// validateSchemaFile
// ---------------------------------------------------------------------------

func TestValidateSchemaFile_MissingFile(t *testing.T) {
	err := validateSchemaFile("/tmp/does-not-exist-spectral-test.json", "schemas/blockchain.schema.json")
	if err != nil {
		t.Fatalf("expected nil for missing file, got %v", err)
	}
}

func TestValidateSchemaFile_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bad-*.json")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	_, _ = f.WriteString("not-json{{{{")
	_ = f.Close()

	err = validateSchemaFile(f.Name(), "schemas/blockchain.schema.json")
	if err == nil {
		t.Fatal("expected error for invalid JSON file")
	}
}

// ---------------------------------------------------------------------------
// apiHealthCmd / apiRoutesCmd / apiAgentsCmd — happy paths via httptest
// ---------------------------------------------------------------------------

func TestApiHealthCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer srv.Close()

	// Capture stdout
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	apiHealthCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from apiHealthCmd")
	}
}

func TestApiRoutesCmd_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"destination":"10.0.0.1:9000","metric":{"latency":5}}]`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	apiRoutesCmd([]string{"--url", srv.URL, "--json"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected JSON output")
	}
}

func TestApiRoutesCmd_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"destination":"10.0.0.1:9000","metric":{"latency":5,"throughput":100}}]`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	apiRoutesCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected table output")
	}
}

func TestApiAgentsCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"agent-1","tenant_id":"default","status":"healthy","capabilities":["inference"]}]`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	apiAgentsCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from apiAgentsCmd")
	}
}

// ---------------------------------------------------------------------------
// validateBlockchain / validateRoutes — JSON file paths
// ---------------------------------------------------------------------------

func TestValidateBlockchain_JSONFile(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := chain.Snapshot()
	data, _ := json.MarshalIndent(blocks, "", "  ")
	f, _ := os.CreateTemp("", "blocks-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	issues, err := validateBlockchain(f.Name(), "/nonexistent.db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestValidateBlockchain_NonExistent(t *testing.T) {
	issues, err := validateBlockchain("/nonexistent.json", "/nonexistent.db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issues != nil {
		t.Fatal("expected nil issues for nonexistent file")
	}
}

func TestValidateRoutes_JSONFile(t *testing.T) {
	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	routes := router.ListRoutes()
	data, _ := json.MarshalIndent(routes, "", "  ")
	f, _ := os.CreateTemp("", "routes-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	issues, err := validateRoutes(f.Name(), "/nonexistent.db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = issues // may or may not have issues depending on schema
}

func TestValidateRoutes_NonExistent(t *testing.T) {
	issues, err := validateRoutes("/nonexistent.json", "/nonexistent.db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issues != nil {
		t.Fatal("expected nil issues for nonexistent file")
	}
}

// ---------------------------------------------------------------------------
// repairBlockchain / repairRoutes
// ---------------------------------------------------------------------------

func TestRepairBlockchain_JSONFile(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := chain.Snapshot()
	data, _ := json.MarshalIndent(blocks, "", "  ")
	f, _ := os.CreateTemp("", "blocks-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	if err := repairBlockchain(f.Name(), "/nonexistent.db"); err != nil {
		t.Fatalf("repairBlockchain: %v", err)
	}
}

func TestRepairBlockchain_NonExistent(t *testing.T) {
	if err := repairBlockchain("/nonexistent.json", "/nonexistent.db"); err != nil {
		t.Fatalf("expected nil for nonexistent: %v", err)
	}
}

func TestRepairRoutes_JSONFile(t *testing.T) {
	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	routes := router.ListRoutes()
	data, _ := json.MarshalIndent(routes, "", "  ")
	f, _ := os.CreateTemp("", "routes-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	if err := repairRoutes(f.Name(), "/nonexistent.db"); err != nil {
		t.Fatalf("repairRoutes: %v", err)
	}
}

func TestRepairRoutes_NonExistent(t *testing.T) {
	if err := repairRoutes("/nonexistent.json", "/nonexistent.db"); err != nil {
		t.Fatalf("expected nil for nonexistent: %v", err)
	}
}

// ---------------------------------------------------------------------------
// readBlocksFromDB / readRoutesFromDB via actual BoltDB
// ---------------------------------------------------------------------------

func TestReadBlocksFromDB_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	db, _ := store.Open(dbPath)
	db.Close()

	blocks, ok, err := readBlocksFromDB(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || blocks != nil {
		t.Fatal("expected ok=false for empty db")
	}
}

func TestReadBlocksFromDB_WithData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	db, _ := store.Open(dbPath)
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	db.WriteBlocks(chain.Snapshot())
	db.Close()

	blocks, ok, err := readBlocksFromDB(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true when blocks exist")
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
}

func TestReadRoutesFromDB_WithData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	db, _ := store.Open(dbPath)
	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	db.WriteRoutes(router.ListRoutes())
	db.Close()

	routes, ok, err := readRoutesFromDB(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true when routes exist")
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
}

// ---------------------------------------------------------------------------
// writeBlocksToDB / writeRoutesToDB
// ---------------------------------------------------------------------------

func TestWriteBlocksToDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	if err := writeBlocksToDB(dbPath, chain.Snapshot()); err != nil {
		t.Fatalf("writeBlocksToDB: %v", err)
	}
	blocks, ok, _ := readBlocksFromDB(dbPath)
	if !ok || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks after write, got ok=%v len=%d", ok, len(blocks))
	}
}

func TestWriteRoutesToDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	if err := writeRoutesToDB(dbPath, router.ListRoutes()); err != nil {
		t.Fatalf("writeRoutesToDB: %v", err)
	}
	routes, ok, _ := readRoutesFromDB(dbPath)
	if !ok || len(routes) != 1 {
		t.Fatalf("expected 1 route after write, got ok=%v len=%d", ok, len(routes))
	}
}

// ---------------------------------------------------------------------------
// writeJSON
// ---------------------------------------------------------------------------

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	type payload struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}
	if err := writeJSON(path, payload{Name: "test", Val: 42}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	data, _ := os.ReadFile(path)
	var got payload
	json.Unmarshal(data, &got)
	if got.Name != "test" || got.Val != 42 {
		t.Fatalf("unexpected: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// pruneExpired
// ---------------------------------------------------------------------------

func TestPruneExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	routes := []routing.Route{
		{Destination: "keep", ExpiresAt: &future},
		{Destination: "drop", ExpiresAt: &past},
		{Destination: "no-expiry"},
	}
	result := pruneExpired(routes)
	if len(result) != 2 {
		t.Fatalf("expected 2 routes after pruning, got %d", len(result))
	}
	for _, r := range result {
		if r.Destination == "drop" {
			t.Fatal("expired route should have been removed")
		}
	}
}

// ---------------------------------------------------------------------------
// validateBlocks / filterValidBlocks
// ---------------------------------------------------------------------------

func TestValidateBlocks_ValidJSON(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	issues := validateBlocks(chain.Snapshot())
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestFilterValidBlocks_AllValidExtra(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := chain.Snapshot()
	valid := filterValidBlocks(blocks)
	if len(valid) != len(blocks) {
		t.Fatalf("all blocks should be valid: got %d / %d", len(valid), len(blocks))
	}
}

// ---------------------------------------------------------------------------
// validateSchemaDoc — schema missing → error; valid JSON → no error on non-strict
// ---------------------------------------------------------------------------

func TestValidateSchemaDoc_SchemaMissing(t *testing.T) {
	err := validateSchemaDoc(map[string]any{"x": 1}, "/nonexistent/schema.json")
	if err == nil {
		t.Fatal("expected error for missing schema file")
	}
}

// ---------------------------------------------------------------------------
// blockchainInspectCmd — happy path via HTTP
// ---------------------------------------------------------------------------

func TestBlockchainInspectCmd_Happy(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := chain.Snapshot()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(blocks)
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	blockchainInspectCmd([]string{"--url", srv.URL, "--index", "0"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from blockchainInspectCmd")
	}
}

// ---------------------------------------------------------------------------
// chainVerifyCmd — healthy chain
// ---------------------------------------------------------------------------

func TestChainVerifyCmd_Happy(t *testing.T) {
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	blocks := chain.Snapshot()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(blocks)
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	chainVerifyCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from chainVerifyCmd")
	}
}

// ---------------------------------------------------------------------------
// tenantListCmd — happy path via HTTP
// ---------------------------------------------------------------------------

func TestTenantListCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tenants": []string{"default", "acme"}})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	tenantListCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from tenantListCmd")
	}
}

// ---------------------------------------------------------------------------
// auditTailCmd — happy path
// ---------------------------------------------------------------------------

func TestAuditTailCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"entries": []map[string]any{
				{"id": 1, "tenant": "default", "method": "POST", "path": "/blockchain/add", "status": 200},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	auditTailCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if len(out) == 0 {
		t.Fatal("expected output from auditTailCmd")
	}
}

// ---------------------------------------------------------------------------
// agentStatusCmd — happy path
// ---------------------------------------------------------------------------

func TestAgentStatusCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Only test non-exit (success) path
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	agentStatusCmd([]string{"--url", srv.URL, "--id", "agent-1", "--status", "healthy"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	_ = string(buf[:n])
}

// ---------------------------------------------------------------------------
// routeBestCmd — DB path
// ---------------------------------------------------------------------------

func TestRouteBestCmd_Happy(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "spectral.db")
	db, _ := store.Open(dbPath)
	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	db.WriteRoutesTenant("default", router.ListRoutes())
	db.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	routeBestCmd([]string{"--db-path", dbPath, "--tenant", "default"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from routeBestCmd")
	}
}

// ---------------------------------------------------------------------------
// agentRegisterCmd
// ---------------------------------------------------------------------------

func TestAgentRegisterCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "agent-1", "status": "healthy"})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	agentRegisterCmd([]string{"--url", srv.URL, "--id", "agent-1", "--capability", "inference"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output")
	}
}

// ---------------------------------------------------------------------------
// blockchainExportCmd — to stdout and to file
// ---------------------------------------------------------------------------

func TestBlockchainExportCmd_Stdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"index":0}]`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	blockchainExportCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output")
	}
}

func TestBlockchainExportCmd_File(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"index":0}]`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "chain.json")

	blockchainExportCmd([]string{"--url", srv.URL, "--out", out})

	data, _ := os.ReadFile(out)
	if len(data) == 0 {
		t.Fatal("expected file to be written")
	}
}

// ---------------------------------------------------------------------------
// routesExportCmd — to stdout and to file
// ---------------------------------------------------------------------------

func TestRoutesExportCmd_Stdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"destination":"10.0.0.1:9000"}]`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	routesExportCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output")
	}
}

func TestRoutesExportCmd_File(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"destination":"10.0.0.1:9000"}]`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "routes.json")

	routesExportCmd([]string{"--url", srv.URL, "--out", out})

	data, _ := os.ReadFile(out)
	if len(data) == 0 {
		t.Fatal("expected file to be written")
	}
}

// ---------------------------------------------------------------------------
// tenantCreateCmd / tenantDeleteCmd
// ---------------------------------------------------------------------------

func TestTenantCreateCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"name": "acme"})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	tenantCreateCmd([]string{"--url", srv.URL, "--name", "acme"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "acme") {
		t.Fatalf("expected 'acme' in output, got: %s", string(buf[:n]))
	}
}

func TestTenantDeleteCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	tenantDeleteCmd([]string{"--url", srv.URL, "--name", "acme"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "acme") {
		t.Fatalf("expected 'acme' in output, got: %s", string(buf[:n]))
	}
}

// ---------------------------------------------------------------------------
// chainSearchCmd
// ---------------------------------------------------------------------------

func TestChainSearchCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"results": []map[string]any{
				{"block_index": 1, "sender": "alice", "recipient": "bob", "amount": 10},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	chainSearchCmd([]string{"--url", srv.URL, "--sender", "alice"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from chainSearchCmd")
	}
}

// ---------------------------------------------------------------------------
// agentRouteCmd
// ---------------------------------------------------------------------------

func TestAgentRouteCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"agents": []map[string]any{
				{"id": "agent-1", "status": "healthy"},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	agentRouteCmd([]string{"--url", srv.URL, "--capability", "inference"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from agentRouteCmd")
	}
}

// ---------------------------------------------------------------------------
// jobsListCmd / jobsSubmitCmd
// ---------------------------------------------------------------------------

func TestJobsListCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"jobs": []map[string]any{
				{"id": "job-1", "agent_id": "agent-1", "capability": "inference", "status": "pending"},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	jobsListCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from jobsListCmd")
	}
}

func TestJobsSubmitCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "job-1", "status": "pending"})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	jobsSubmitCmd([]string{"--url", srv.URL, "--capability", "inference"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from jobsSubmitCmd")
	}
}

// ---------------------------------------------------------------------------
// importBlockchainCmd / importRoutesCmd
// ---------------------------------------------------------------------------

func TestImportBlockchainCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"imported": 2})
	}))
	defer srv.Close()

	// Write a JSON file
	chain := blockchain.NewBlockchain()
	chain.AddBlock([]blockchain.Transaction{{Sender: "a", Recipient: "b", Amount: 1}})
	data, _ := json.Marshal(chain.Snapshot())
	f, _ := os.CreateTemp("", "chain-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	importBlockchainCmd([]string{"--url", srv.URL, "--in", f.Name()})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from importBlockchainCmd")
	}
}

func TestImportRoutesCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"imported": 1})
	}))
	defer srv.Close()

	router := routing.NewRoutingEngine()
	router.AddRoute("10.0.0.1:9000", routing.RouteMetric{Latency: 5, Throughput: 100})
	data, _ := json.Marshal(router.ListRoutes())
	f, _ := os.CreateTemp("", "routes-*.json")
	f.Write(data)
	f.Close()
	defer os.Remove(f.Name())

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	importRoutesCmd([]string{"--url", srv.URL, "--in", f.Name()})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if len(buf[:n]) == 0 {
		t.Fatal("expected output from importRoutesCmd")
	}
}

// ---------------------------------------------------------------------------
// metricsCmd
// ---------------------------------------------------------------------------

func TestMetricsCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"timestamp":      "2026-04-09T00:00:00Z",
			"uptime_seconds": 3700,
			"jobs":           5,
			"tenants": []map[string]any{
				{"tenant": "default", "blocks": 10, "routes": 3, "agents": 2},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	metricsCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "Uptime") {
		t.Fatalf("expected uptime info, got: %s", string(buf[:n]))
	}
}

// ---------------------------------------------------------------------------
// kvGetCmd / kvSetCmd / kvDeleteCmd / kvListCmd
// ---------------------------------------------------------------------------

func TestKvGetCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"key": "mykey", "value": "myval", "tenant": "default", "updated_at": "2026-04-09T00:00:00Z",
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvGetCmd([]string{"--url", srv.URL, "--key", "mykey"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "myval") {
		t.Fatalf("expected 'myval' in output, got: %s", string(buf[:n]))
	}
}

func TestKvSetCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvSetCmd([]string{"--url", srv.URL, "--key", "mykey", "--value", "myval"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "mykey") {
		t.Fatalf("expected 'mykey' in output, got: %s", string(buf[:n]))
	}
}

func TestKvSetCmd_WithTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvSetCmd([]string{"--url", srv.URL, "--key", "mykey", "--value", "myval", "--ttl", "10m"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "mykey") {
		t.Fatalf("expected 'mykey' in output, got: %s", string(buf[:n]))
	}
}

func TestKvDeleteCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvDeleteCmd([]string{"--url", srv.URL, "--key", "mykey"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "mykey") {
		t.Fatalf("expected 'mykey' in output, got: %s", string(buf[:n]))
	}
}

func TestKvListCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"entries": []map[string]any{
				{"key": "k1", "value": "v1"},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvListCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "k1") {
		t.Fatalf("expected 'k1' in output, got: %s", string(buf[:n]))
	}
}

func TestKvListCmd_WithPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"count": 0, "entries": []map[string]any{}})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	kvListCmd([]string{"--url", srv.URL, "--prefix", "app:"})
	w.Close()
	os.Stdout = old
	buf := make([]byte, 4096)
	r.Read(buf)
}

// ---------------------------------------------------------------------------
// searchCmd
// ---------------------------------------------------------------------------

func TestSearchCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"query":  "alice",
			"agents": []map[string]any{{"id": "a1", "status": "healthy"}},
			"routes": []map[string]any{},
			"transactions": []map[string]any{
				{"block_index": 1, "sender": "alice", "recipient": "bob"},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	searchCmd([]string{"--url", srv.URL, "--q", "alice"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "alice") {
		t.Fatalf("expected 'alice' in output, got: %s", string(buf[:n]))
	}
}

func TestSearchCmd_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"query": "nope", "agents": []any{}, "routes": []any{}, "transactions": []any{},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	searchCmd([]string{"--url", srv.URL, "--q", "nope"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "No results") {
		t.Fatalf("expected 'No results' in output, got: %s", string(buf[:n]))
	}
}

// ---------------------------------------------------------------------------
// routesFilterCmd
// ---------------------------------------------------------------------------

func TestRoutesFilterCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"routes": []map[string]any{
				{
					"destination": "10.0.0.1:9000",
					"metric":      map[string]any{"latency": 3, "throughput": 200},
					"satellite":   false,
				},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	routesFilterCmd([]string{"--url", srv.URL, "--max-latency", "10"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "10.0.0.1") {
		t.Fatalf("expected route in output, got: %s", string(buf[:n]))
	}
}

// ---------------------------------------------------------------------------
// notifyListCmd / notifyAddCmd / notifyDeleteCmd
// ---------------------------------------------------------------------------

func TestNotifyListCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"rules": []map[string]any{
				{"id": "r1", "name": "test-rule", "webhook_url": "http://example.com", "active": true, "fired_total": 5},
			},
		})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	notifyListCmd([]string{"--url", srv.URL})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "test-rule") {
		t.Fatalf("expected 'test-rule' in output, got: %s", string(buf[:n]))
	}
}

func TestNotifyAddCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "r1", "name": "my-rule"})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	notifyAddCmd([]string{"--url", srv.URL, "--name", "my-rule", "--webhook-url", "http://example.com/hook"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "my-rule") {
		t.Fatalf("expected 'my-rule' in output, got: %s", string(buf[:n]))
	}
}

func TestNotifyAddCmd_WithEventTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": "r2", "name": "block-rule"})
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	notifyAddCmd([]string{
		"--url", srv.URL,
		"--name", "block-rule",
		"--webhook-url", "http://example.com/hook",
		"--event-types", "block_added,agent_registered",
	})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "block-rule") {
		t.Fatalf("expected 'block-rule' in output, got: %s", string(buf[:n]))
	}
}

func TestNotifyDeleteCmd_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	notifyDeleteCmd([]string{"--url", srv.URL, "--id", "r1"})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "r1") {
		t.Fatalf("expected 'r1' in output, got: %s", string(buf[:n]))
	}
}
