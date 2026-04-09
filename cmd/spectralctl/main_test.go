package main

import (
	"net/http"
	"net/http/httptest"
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
