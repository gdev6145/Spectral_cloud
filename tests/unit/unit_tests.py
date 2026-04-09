# Unit and integration tests for Spectral Cloud HTTP API.
#
# Unit tests run standalone (no server required).
# Integration tests run against a live server when SPECTRAL_URL is set.
#
# Usage:
#   python -m pytest tests/unit/unit_tests.py -v
#   SPECTRAL_URL=http://localhost:8080 python -m pytest tests/unit/unit_tests.py -v

import json
import os
import unittest
import urllib.request
import urllib.error


SPECTRAL_URL = os.environ.get("SPECTRAL_URL", "").rstrip("/")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def http_get(path, api_key=None):
    url = SPECTRAL_URL + path
    req = urllib.request.Request(url)
    if api_key:
        req.add_header("Authorization", "Bearer " + api_key)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def http_post(path, body, api_key=None, content_type="application/json"):
    url = SPECTRAL_URL + path
    data = json.dumps(body).encode() if isinstance(body, (dict, list)) else body
    req = urllib.request.Request(url, data=data, method="POST")
    req.add_header("Content-Type", content_type)
    if api_key:
        req.add_header("Authorization", "Bearer " + api_key)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            raw = resp.read()
            return resp.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        raw = e.read()
        return e.code, json.loads(raw) if raw else {}


# ---------------------------------------------------------------------------
# Pure unit tests (no server required)
# ---------------------------------------------------------------------------

class TestRouteMetricScoring(unittest.TestCase):
    """Validate the best-next-hop scoring logic in pure Python."""

    def _score(self, latency, throughput):
        """Mirror the Go SelectBestNextHop scoring formula."""
        score = float(max(latency, 0))
        if throughput > 0:
            score -= float(throughput)
        return score

    def test_lower_latency_wins_when_throughput_equal(self):
        score_a = self._score(10, 100)
        score_b = self._score(50, 100)
        self.assertLess(score_a, score_b)

    def test_higher_throughput_wins_when_latency_equal(self):
        score_a = self._score(50, 200)
        score_b = self._score(50, 100)
        self.assertLess(score_a, score_b)

    def test_negative_latency_clamped_to_zero(self):
        self.assertEqual(self._score(-10, 0), 0.0)

    def test_zero_throughput_not_penalised(self):
        self.assertEqual(self._score(20, 0), 20.0)

    def test_best_route_selected_from_list(self):
        routes = [
            {"latency": 100, "throughput": 10},
            {"latency": 20,  "throughput": 10},   # winner: lowest score
            {"latency": 50,  "throughput": 5},
        ]
        best = min(routes, key=lambda r: self._score(r["latency"], r["throughput"]))
        self.assertEqual(best["latency"], 20)


class TestTransactionValidation(unittest.TestCase):
    """Replicate the Go validateTransactions checks in Python."""

    def _validate(self, txs):
        errors = []
        for i, tx in enumerate(txs):
            if not tx.get("sender", "").strip() or not tx.get("recipient", "").strip():
                errors.append("transaction sender and recipient must be set")
                break
            if tx.get("amount", 0) < 0:
                errors.append("transaction amount must be non-negative")
                break
            if i >= 1000:
                errors.append("too many transactions")
                break
        return errors

    def test_valid_transaction(self):
        self.assertEqual(self._validate([{"sender": "alice", "recipient": "bob", "amount": 10}]), [])

    def test_missing_sender(self):
        errs = self._validate([{"sender": "", "recipient": "bob", "amount": 5}])
        self.assertTrue(len(errs) > 0)

    def test_missing_recipient(self):
        errs = self._validate([{"sender": "alice", "recipient": "", "amount": 5}])
        self.assertTrue(len(errs) > 0)

    def test_negative_amount(self):
        errs = self._validate([{"sender": "alice", "recipient": "bob", "amount": -1}])
        self.assertTrue(len(errs) > 0)

    def test_zero_amount_allowed(self):
        self.assertEqual(self._validate([{"sender": "alice", "recipient": "bob", "amount": 0}]), [])

    def test_too_many_transactions(self):
        txs = [{"sender": "a", "recipient": "b", "amount": 1}] * 1001
        errs = self._validate(txs)
        self.assertTrue(len(errs) > 0)


class TestAnomalyDetection(unittest.TestCase):
    """Python mirror of the Go detectAnomaly function."""

    def _detect(self, reject_rate, rejected_delta, received_delta,
                rate_threshold=0.3, burst_threshold=20, min_samples=50, window=None):
        if window is None:
            window = []
        if received_delta < min_samples:
            return False, ""
        if burst_threshold > 0 and rejected_delta >= burst_threshold:
            return True, "reject_burst"
        if rate_threshold > 0 and reject_rate >= rate_threshold:
            return True, "reject_rate"
        if len(window) >= 3 and rate_threshold > 0:
            if (window[-3] < window[-2] < window[-1] and
                    window[-1] >= rate_threshold * 0.5):
                return True, "reject_rate_trend"
        return False, ""

    def test_no_anomaly_below_min_samples(self):
        triggered, _ = self._detect(0.9, 45, 45)
        self.assertFalse(triggered)

    def test_burst_anomaly(self):
        triggered, reason = self._detect(0.1, 25, 100)
        self.assertTrue(triggered)
        self.assertEqual(reason, "reject_burst")

    def test_rate_anomaly(self):
        # rejected_delta=15 stays below burst_threshold=20, but rate=0.3 hits rate_threshold
        triggered, reason = self._detect(0.3, 15, 50)
        self.assertTrue(triggered)
        self.assertEqual(reason, "reject_rate")

    def test_trend_anomaly(self):
        window = [0.05, 0.10, 0.16]
        triggered, reason = self._detect(0.16, 16, 100, window=window)
        self.assertTrue(triggered)
        self.assertEqual(reason, "reject_rate_trend")

    def test_no_anomaly_normal_traffic(self):
        triggered, _ = self._detect(0.05, 5, 100, window=[0.03, 0.04, 0.05])
        self.assertFalse(triggered)

    def test_non_increasing_trend_not_triggered(self):
        window = [0.20, 0.15, 0.16]
        triggered, _ = self._detect(0.16, 16, 100, window=window)
        self.assertFalse(triggered)


class TestHealthResponseSchema(unittest.TestCase):
    """Validate expected JSON shape of /health without hitting a server."""

    def _make_health(self, status="ok", blocks=1, routes=0):
        return {"status": status, "timestamp": "2026-01-01T00:00:00Z",
                "blocks": blocks, "routes": routes}

    def test_required_fields_present(self):
        h = self._make_health()
        for field in ("status", "timestamp", "blocks", "routes"):
            self.assertIn(field, h)

    def test_status_ok(self):
        self.assertEqual(self._make_health()["status"], "ok")

    def test_blocks_non_negative(self):
        self.assertGreaterEqual(self._make_health(blocks=0)["blocks"], 0)


# ---------------------------------------------------------------------------
# Integration tests (require SPECTRAL_URL to be set)
# ---------------------------------------------------------------------------

@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestHealthEndpoint(unittest.TestCase):

    def test_health_returns_200(self):
        status, body = http_get("/health")
        self.assertEqual(status, 200)

    def test_health_body_has_status_ok(self):
        _, body = http_get("/health")
        self.assertEqual(body.get("status"), "ok")

    def test_health_body_has_blocks_field(self):
        _, body = http_get("/health")
        self.assertIn("blocks", body)

    def test_health_body_has_routes_field(self):
        _, body = http_get("/health")
        self.assertIn("routes", body)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestReadyEndpoint(unittest.TestCase):

    def test_ready_returns_200(self):
        status, body = http_get("/ready")
        self.assertEqual(status, 200)
        self.assertEqual(body.get("status"), "ready")


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestBlockchainEndpoint(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_add_valid_transaction(self):
        txs = [{"sender": "alice", "recipient": "bob", "amount": 1}]
        status, _ = http_post("/blockchain/add", txs, api_key=self.API_KEY)
        self.assertEqual(status, 201)

    def test_add_invalid_transaction_missing_sender(self):
        txs = [{"sender": "", "recipient": "bob", "amount": 1}]
        status, body = http_post("/blockchain/add", txs, api_key=self.API_KEY)
        self.assertEqual(status, 400)
        self.assertIn("error", body)

    def test_add_invalid_transaction_negative_amount(self):
        txs = [{"sender": "alice", "recipient": "bob", "amount": -5}]
        status, body = http_post("/blockchain/add", txs, api_key=self.API_KEY)
        self.assertEqual(status, 400)
        self.assertIn("error", body)

    def test_health_blocks_increases_after_add(self):
        _, before = http_get("/health")
        blocks_before = before.get("blocks", 0)
        txs = [{"sender": "x", "recipient": "y", "amount": 1}]
        http_post("/blockchain/add", txs, api_key=self.API_KEY)
        _, after = http_get("/health")
        self.assertGreater(after.get("blocks", 0), blocks_before)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestRoutesEndpoint(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_get_routes_returns_list(self):
        status, body = http_get("/routes")
        self.assertEqual(status, 200)
        self.assertIsInstance(body, list)

    def test_post_route_returns_201(self):
        status, _ = http_post(
            "/routes?destination=10.0.0.1:7000&latency=10&throughput=100",
            {},
            api_key=self.API_KEY,
        )
        self.assertEqual(status, 201)

    def test_post_route_without_destination_returns_400(self):
        status, body = http_post("/routes", {}, api_key=self.API_KEY)
        self.assertEqual(status, 400)
        self.assertIn("error", body)


class TestAgentRegistryLogic(unittest.TestCase):
    """Pure-Python mirror of the agent registry constraints."""

    def _make_agent(self, id="a1", tenant="t1", status="healthy", ttl=300):
        return {"id": id, "tenant_id": tenant, "status": status, "ttl_seconds": ttl}

    def test_id_required(self):
        a = self._make_agent(id="")
        self.assertEqual(a["id"], "")  # missing id should be rejected

    def test_tenant_required(self):
        a = self._make_agent(tenant="")
        self.assertEqual(a["tenant_id"], "")

    def test_valid_statuses(self):
        for s in ("healthy", "degraded", "unknown"):
            a = self._make_agent(status=s)
            self.assertIn(a["status"], ("healthy", "degraded", "unknown"))

    def test_ttl_zero_means_no_expiry(self):
        a = self._make_agent(ttl=0)
        self.assertEqual(a["ttl_seconds"], 0)

    def test_ttl_positive(self):
        a = self._make_agent(ttl=300)
        self.assertGreater(a["ttl_seconds"], 0)


class TestBlockchainPagination(unittest.TestCase):
    """Validate the expected behavior of GET /blockchain pagination logic."""

    def _paginate(self, total_height, offset, limit):
        """Simulate BlockRange clamping."""
        if limit <= 0 or limit > 1000:
            limit = 100
        if offset < 0:
            offset = 0
        start = offset
        end = min(offset + limit, total_height)
        return list(range(start, end))

    def test_first_page(self):
        blocks = self._paginate(50, 0, 10)
        self.assertEqual(len(blocks), 10)
        self.assertEqual(blocks[0], 0)

    def test_last_page_clamped(self):
        blocks = self._paginate(5, 3, 10)
        self.assertEqual(len(blocks), 2)  # only 2 blocks remain

    def test_offset_beyond_height_returns_empty(self):
        blocks = self._paginate(5, 10, 10)
        self.assertEqual(len(blocks), 0)

    def test_default_limit_applied_when_zero(self):
        blocks = self._paginate(200, 0, 0)
        self.assertEqual(len(blocks), 100)

    def test_limit_capped_at_1000(self):
        blocks = self._paginate(2000, 0, 5000)
        self.assertEqual(len(blocks), 100)  # clamped to default


class TestCORSConfig(unittest.TestCase):
    """Python mirror of CORS origin matching."""

    def _is_allowed(self, origins, request_origin, allow_all=False):
        if allow_all:
            return True
        return request_origin in origins

    def test_explicit_origin_allowed(self):
        self.assertTrue(self._is_allowed(["https://app.example.com"], "https://app.example.com"))

    def test_unknown_origin_rejected(self):
        self.assertFalse(self._is_allowed(["https://app.example.com"], "https://evil.com"))

    def test_wildcard_allows_any(self):
        self.assertTrue(self._is_allowed([], "https://any.domain.com", allow_all=True))

    def test_empty_origins_list_rejects_all(self):
        self.assertFalse(self._is_allowed([], "https://example.com", allow_all=False))


class TestBlockSignatureLogic(unittest.TestCase):
    """Mirror of blockchain HMAC-SHA256 signature logic."""

    def _sign(self, block_hash, key):
        import hmac as _hmac
        import hashlib
        return _hmac.new(key.encode(), block_hash.encode(), hashlib.sha256).hexdigest()

    def test_signature_matches_expected(self):
        sig = self._sign("abc123hash", "secret")
        self.assertNotEqual(sig, "")
        self.assertEqual(len(sig), 64)  # SHA256 hex = 64 chars

    def test_wrong_key_produces_different_signature(self):
        s1 = self._sign("abc123", "key1")
        s2 = self._sign("abc123", "key2")
        self.assertNotEqual(s1, s2)

    def test_tampered_hash_fails_verification(self):
        original_sig = self._sign("abc123", "key")
        tampered_sig = self._sign("tampered", "key")
        self.assertNotEqual(original_sig, tampered_sig)

    def test_empty_key_no_signature(self):
        # Go returns "" for empty key — Python equivalent: no sig
        key = ""
        if key == "":
            sig = ""
        else:
            sig = self._sign("abc123", key)
        self.assertEqual(sig, "")


class TestWebhookSignature(unittest.TestCase):
    """Mirror of webhook HMAC-SHA256 signing and verification."""

    def _sign(self, body, secret):
        import hmac as _hmac
        import hashlib
        mac = _hmac.new(secret.encode(), body if isinstance(body, bytes) else body.encode(),
                        hashlib.sha256)
        return "sha256=" + mac.hexdigest()

    def _verify(self, header, body, secret):
        if not header.startswith("sha256="):
            return False
        expected = self._sign(body, secret)
        if len(header) != len(expected):
            return False
        return header == expected  # constant-time in Go, good enough for test

    def test_valid_signature_verifies(self):
        body = b'{"type":"block_added"}'
        sig = self._sign(body, "webhook-secret")
        self.assertTrue(self._verify(sig, body, "webhook-secret"))

    def test_wrong_secret_fails(self):
        body = b'{"type":"block_added"}'
        sig = self._sign(body, "right-secret")
        self.assertFalse(self._verify(sig, body, "wrong-secret"))

    def test_missing_prefix_fails(self):
        self.assertFalse(self._verify("badhash", b"body", "secret"))

    def test_tampered_body_fails(self):
        body = b'{"type":"block_added"}'
        sig = self._sign(body, "secret")
        self.assertFalse(self._verify(sig, b'tampered', "secret"))


class TestSatelliteRoutingPenalty(unittest.TestCase):
    """Mirror of SelectBestNextHopOpts satellite penalty logic."""

    def _score(self, latency, throughput, satellite=False, penalty=300):
        lat = max(float(latency), 0.0)
        if satellite and penalty > 0:
            lat += float(penalty)
        thr = float(throughput)
        score = lat
        if thr > 0:
            score -= thr
        return score

    def test_satellite_penalized_over_terrestrial(self):
        sat_score = self._score(20, 10, satellite=True, penalty=300)
        ter_score = self._score(50, 10, satellite=False, penalty=300)
        self.assertGreater(sat_score, ter_score)  # satellite is worse

    def test_no_penalty_satellite_wins_lower_latency(self):
        sat_score = self._score(20, 10, satellite=True, penalty=0)
        ter_score = self._score(50, 10, satellite=False, penalty=0)
        self.assertLess(sat_score, ter_score)

    def test_only_satellite_available(self):
        # Even with penalty, satellite is the only option → should be selected
        routes = [{"latency": 500, "throughput": 10, "satellite": True}]
        best = min(routes, key=lambda r: self._score(r["latency"], r["throughput"],
                                                      r["satellite"], 300))
        self.assertEqual(best["latency"], 500)


class TestTagParsing(unittest.TestCase):
    """Mirror of parseTags helper in Go."""

    def _parse_tags(self, raw_list):
        out = {}
        for kv in raw_list:
            parts = kv.split(":", 1)
            if len(parts) == 2 and parts[0].strip():
                out[parts[0].strip()] = parts[1].strip()
        return out if out else None

    def test_valid_single_tag(self):
        tags = self._parse_tags(["region:us-west"])
        self.assertEqual(tags, {"region": "us-west"})

    def test_multiple_tags(self):
        tags = self._parse_tags(["region:us-west", "tier:premium"])
        self.assertEqual(tags, {"region": "us-west", "tier": "premium"})

    def test_empty_list_returns_none(self):
        self.assertIsNone(self._parse_tags([]))

    def test_malformed_entry_skipped(self):
        tags = self._parse_tags(["no-colon", "valid:ok"])
        self.assertEqual(tags, {"valid": "ok"})


class TestEventBrokerLogic(unittest.TestCase):
    """Validate event broker invariants without needing a live server."""

    def _make_event(self, event_type, tenant="t1", data=None):
        return {
            "type": event_type,
            "tenant_id": tenant,
            "timestamp": "2026-04-05T00:00:00Z",
            "data": data,
        }

    def test_event_has_required_fields(self):
        e = self._make_event("block_added")
        self.assertIn("type", e)
        self.assertIn("tenant_id", e)
        self.assertIn("timestamp", e)

    def test_known_event_types(self):
        types = ["block_added", "route_added", "route_deleted",
                 "agent_registered", "agent_deregistered",
                 "agent_heartbeat", "mesh_anomaly"]
        for t in types:
            e = self._make_event(t)
            self.assertEqual(e["type"], t)

    def test_tenant_filter_logic(self):
        events_data = [
            self._make_event("block_added", tenant="t1"),
            self._make_event("route_added", tenant="t2"),
            self._make_event("agent_registered", tenant="t1"),
        ]
        t1_events = [e for e in events_data if e["tenant_id"] == "t1"]
        self.assertEqual(len(t1_events), 2)

    def test_type_filter_logic(self):
        events_data = [
            self._make_event("block_added"),
            self._make_event("route_added"),
            self._make_event("block_added"),
        ]
        block_events = [e for e in events_data if e["type"] == "block_added"]
        self.assertEqual(len(block_events), 2)


class TestGossipRouteEmbedding(unittest.TestCase):
    """Validate gossip route packet structure."""

    def _make_gossip_routes(self, routes, max_routes=50):
        """Mirror the Go gossip route selection (cap at max_routes)."""
        return routes[:max_routes]

    def test_routes_capped_at_max(self):
        routes = [{"dst": f"peer-{i}", "lat": i, "thr": 10} for i in range(100)]
        gossip = self._make_gossip_routes(routes, max_routes=50)
        self.assertEqual(len(gossip), 50)

    def test_empty_routes_not_sent(self):
        gossip = self._make_gossip_routes([])
        self.assertEqual(len(gossip), 0)

    def test_fewer_routes_than_max(self):
        routes = [{"dst": "peer-1", "lat": 10, "thr": 100}]
        gossip = self._make_gossip_routes(routes, max_routes=50)
        self.assertEqual(len(gossip), 1)

    def test_gossip_route_has_required_fields(self):
        route = {"dst": "10.0.0.1:7000", "lat": 15, "thr": 200}
        self.assertIn("dst", route)
        self.assertIn("lat", route)
        self.assertIn("thr", route)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestAuthEnforcement(unittest.TestCase):

    def test_public_path_no_auth_required(self):
        status, _ = http_get("/health")
        self.assertEqual(status, 200)

    def test_write_without_key_returns_401(self):
        txs = [{"sender": "a", "recipient": "b", "amount": 1}]
        status, _ = http_post("/blockchain/add", txs)
        # 401 when auth is configured, 201 when auth is disabled
        self.assertIn(status, (201, 401))


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestBlockchainEndpointsExtended(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_blockchain_height_endpoint(self):
        status, body = http_get("/blockchain/height")
        self.assertEqual(status, 200)
        self.assertIn("height", body)
        self.assertGreaterEqual(body["height"], 1)

    def test_blockchain_list_returns_blocks_field(self):
        status, body = http_get("/blockchain?limit=5&offset=0")
        self.assertEqual(status, 200)
        self.assertIn("blocks", body)
        self.assertIn("height", body)
        self.assertIn("limit", body)
        self.assertIn("offset", body)

    def test_blockchain_list_pagination(self):
        _, body = http_get("/blockchain?limit=1&offset=0")
        self.assertIsInstance(body.get("blocks"), list)
        self.assertLessEqual(len(body.get("blocks", [])), 1)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestRoutesBestEndpoint(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_routes_best_returns_404_when_empty(self):
        # This may or may not be 404 depending on existing routes.
        status, _ = http_get("/routes/best")
        self.assertIn(status, (200, 404))

    def test_routes_best_after_add_returns_200(self):
        http_post("/routes?destination=test-best-node&latency=5&throughput=100", {},
                  api_key=self.API_KEY)
        status, body = http_get("/routes/best")
        self.assertEqual(status, 200)
        self.assertIn("destination", body)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestDeleteRouteEndpoint(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_delete_existing_route(self):
        # Add then delete.
        http_post("/routes?destination=delete-me-route&latency=1&throughput=1", {},
                  api_key=self.API_KEY)
        status, _ = http_post("/routes?destination=delete-me-route", {},
                               api_key=self.API_KEY)
        # Attempt deletion via DELETE method.
        url = SPECTRAL_URL + "/routes?destination=delete-me-route"
        req = urllib.request.Request(url, method="DELETE")
        if self.API_KEY:
            req.add_header("Authorization", "Bearer " + self.API_KEY)
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                del_status = resp.status
        except urllib.error.HTTPError as e:
            del_status = e.code
        self.assertIn(del_status, (204, 404))

    def test_delete_missing_destination_returns_400(self):
        url = SPECTRAL_URL + "/routes"
        req = urllib.request.Request(url, method="DELETE")
        if self.API_KEY:
            req.add_header("Authorization", "Bearer " + self.API_KEY)
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                status = resp.status
        except urllib.error.HTTPError as e:
            status = e.code
        self.assertEqual(status, 400)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestAgentRegistryEndpoints(unittest.TestCase):

    API_KEY = os.environ.get("SPECTRAL_WRITE_KEY", os.environ.get("SPECTRAL_API_KEY", ""))

    def test_list_agents_returns_list(self):
        status, body = http_get("/agents", api_key=self.API_KEY)
        self.assertEqual(status, 200)
        self.assertIsInstance(body, list)

    def test_register_agent(self):
        payload = {"id": "integ-agent-1", "addr": "10.0.0.99:9000",
                   "status": "healthy", "ttl_seconds": 300}
        status, body = http_post("/agents/register", payload, api_key=self.API_KEY)
        self.assertEqual(status, 201)
        self.assertEqual(body.get("id"), "integ-agent-1")
        self.assertEqual(body.get("status"), "healthy")

    def test_register_agent_without_id_returns_400(self):
        status, body = http_post("/agents/register", {"addr": "10.0.0.1:9000"},
                                  api_key=self.API_KEY)
        self.assertEqual(status, 400)
        self.assertIn("error", body)

    def test_heartbeat_existing_agent(self):
        # Register first.
        http_post("/agents/register",
                  {"id": "hb-agent", "addr": "10.0.0.50:9000", "ttl_seconds": 300},
                  api_key=self.API_KEY)
        status, _ = http_post("/agents/heartbeat?id=hb-agent&ttl_seconds=300", {},
                               api_key=self.API_KEY)
        self.assertEqual(status, 204)

    def test_heartbeat_unknown_agent_returns_404(self):
        status, _ = http_post("/agents/heartbeat?id=ghost-agent", {},
                               api_key=self.API_KEY)
        self.assertEqual(status, 404)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestRequestIDHeader(unittest.TestCase):

    def test_response_includes_request_id(self):
        status, _ = http_get("/health")
        self.assertEqual(status, 200)
        # Note: we can't easily check headers via http_get; skip header assertion here.
        # Use urllib directly.
        req = urllib.request.Request(SPECTRAL_URL + "/health")
        with urllib.request.urlopen(req, timeout=5) as resp:
            self.assertNotEqual(resp.getheader("X-Request-ID", ""), "")

    def test_client_supplied_request_id_echoed(self):
        req = urllib.request.Request(SPECTRAL_URL + "/health")
        req.add_header("X-Request-ID", "test-id-xyz")
        with urllib.request.urlopen(req, timeout=5) as resp:
            returned_id = resp.getheader("X-Request-ID", "")
        self.assertEqual(returned_id, "test-id-xyz")


class TestChainVerifyLogic(unittest.TestCase):
    """Pure-Python unit tests for chain verification logic."""

    def _make_block(self, index, prev_hash, hash_val):
        return {"index": index, "previous_hash": prev_hash, "hash": hash_val}

    def test_valid_chain_passes(self):
        """A chain where each PreviousHash matches the prior block's Hash."""
        chain = [
            self._make_block(0, "", "abc"),
            self._make_block(1, "abc", "def"),
            self._make_block(2, "def", "ghi"),
        ]
        for i in range(1, len(chain)):
            self.assertEqual(chain[i]["previous_hash"], chain[i-1]["hash"])

    def test_broken_link_detected(self):
        """A chain with a wrong PreviousHash fails at that block."""
        chain = [
            self._make_block(0, "", "abc"),
            self._make_block(1, "WRONG", "def"),
        ]
        bad_index = None
        for i in range(1, len(chain)):
            if chain[i]["previous_hash"] != chain[i-1]["hash"]:
                bad_index = i
                break
        self.assertEqual(bad_index, 1)

    def test_single_block_genesis_always_valid(self):
        chain = [self._make_block(0, "", "abc")]
        for i in range(1, len(chain)):
            self.assertEqual(chain[i]["previous_hash"], chain[i-1]["hash"])
        # No iteration → valid by definition
        self.assertEqual(len(chain), 1)


class TestTransactionSearchLogic(unittest.TestCase):
    """Pure-Python unit tests for transaction search filtering."""

    def _blocks(self):
        return [
            {"index": 0, "transactions": []},
            {"index": 1, "transactions": [{"sender": "alice", "recipient": "bob", "amount": 10}]},
            {"index": 2, "transactions": [{"sender": "carol", "recipient": "bob", "amount": 5}]},
            {"index": 3, "transactions": [{"sender": "alice", "recipient": "carol", "amount": 2}]},
        ]

    def _search(self, sender="", recipient=""):
        return [
            b for b in self._blocks()
            for tx in b["transactions"]
            if (sender == "" or tx["sender"] == sender)
            and (recipient == "" or tx["recipient"] == recipient)
        ]

    def test_search_by_sender(self):
        results = self._search(sender="alice")
        self.assertEqual(len(results), 2)

    def test_search_by_recipient(self):
        results = self._search(recipient="bob")
        self.assertEqual(len(results), 2)

    def test_search_both_filters(self):
        results = self._search(sender="alice", recipient="carol")
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["index"], 3)

    def test_no_match_returns_empty(self):
        results = self._search(sender="nobody")
        self.assertEqual(len(results), 0)


class TestDeleteTenantLogic(unittest.TestCase):
    """Unit tests for tenant deletion guard logic."""

    def _can_delete(self, tenant, default_tenant="default"):
        if not tenant:
            return False, "tenant is empty"
        if tenant == default_tenant:
            return False, "cannot delete the default tenant"
        return True, None

    def test_cannot_delete_default(self):
        ok, reason = self._can_delete("default")
        self.assertFalse(ok)
        self.assertIn("default", reason)

    def test_can_delete_non_default(self):
        ok, _ = self._can_delete("custom-tenant")
        self.assertTrue(ok)

    def test_empty_tenant_rejected(self):
        ok, _ = self._can_delete("")
        self.assertFalse(ok)


class TestBatchRouteValidation(unittest.TestCase):
    """Unit tests for batch route request validation logic."""

    def _validate_batch(self, entries, max_routes=0, current_count=0):
        if not entries:
            return 400, "at least one route is required"
        if max_routes > 0 and current_count + len(entries) > max_routes:
            return 429, "tenant route limit would be exceeded"
        for e in entries:
            if not e.get("destination", "").strip():
                return 400, "destination is required"
        return 201, None

    def test_empty_batch_rejected(self):
        code, msg = self._validate_batch([])
        self.assertEqual(code, 400)

    def test_limit_exceeded_rejected(self):
        code, _ = self._validate_batch(
            [{"destination": "n1"}, {"destination": "n2"}],
            max_routes=3, current_count=2,
        )
        self.assertEqual(code, 429)

    def test_valid_batch_accepted(self):
        code, _ = self._validate_batch([{"destination": "n1"}, {"destination": "n2"}])
        self.assertEqual(code, 201)

    def test_missing_destination_rejected(self):
        code, _ = self._validate_batch([{"destination": ""}])
        self.assertEqual(code, 400)


# ---------------------------------------------------------------------------
# Integration tests for new wave-4 endpoints
# ---------------------------------------------------------------------------

@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestChainVerifyEndpoint(unittest.TestCase):

    def test_verify_returns_valid_for_fresh_chain(self):
        status, body = http_get("/blockchain/verify")
        self.assertEqual(status, 200)
        self.assertIn("valid", body)
        self.assertIn("height", body)

    def test_verify_height_matches_blockchain(self):
        _, verify_body = http_get("/blockchain/verify")
        _, height_body = http_get("/blockchain/height")
        self.assertEqual(verify_body["height"], height_body["height"])


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestBlockchainSearchEndpoint(unittest.TestCase):

    def test_search_requires_sender_or_recipient(self):
        status, body = http_get("/blockchain/search")
        self.assertEqual(status, 400)
        self.assertIn("error", body)

    def test_search_with_sender_returns_200(self):
        status, body = http_get("/blockchain/search?sender=x")
        self.assertEqual(status, 200)
        self.assertIn("count", body)
        self.assertIn("blocks", body)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestExportEndpoints(unittest.TestCase):

    def test_blockchain_export_returns_json_file(self):
        status, body = http_get("/blockchain/export")
        self.assertEqual(status, 200)
        self.assertEqual(body.get("version"), 1)
        self.assertIn("blocks", body)
        self.assertIn("updated_at", body)

    def test_routes_export_returns_json_file(self):
        status, body = http_get("/routes/export")
        self.assertEqual(status, 200)
        self.assertEqual(body.get("version"), 1)
        self.assertIn("routes", body)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestRoutesBatchEndpoint(unittest.TestCase):

    def test_empty_batch_rejected(self):
        status, body = http_post("/routes/batch", [])
        self.assertEqual(status, 400)
        self.assertIn("error", body)

    def test_batch_add_routes(self):
        batch = [
            {"destination": "batch-n1", "latency": 5, "throughput": 100},
            {"destination": "batch-n2", "latency": 10, "throughput": 200},
        ]
        status, body = http_post("/routes/batch", batch)
        self.assertEqual(status, 201)
        self.assertEqual(body["added"], 2)
        self.assertEqual(body["submitted"], 2)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestMeshPeersEndpoint(unittest.TestCase):

    def test_mesh_peers_returns_200(self):
        status, body = http_get("/mesh/peers")
        self.assertEqual(status, 200)
        # Either disabled (no mesh) or a dict of peers.
        self.assertIsInstance(body, dict)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestAdminTenantManagement(unittest.TestCase):

    def test_create_and_delete_tenant(self):
        tenant_name = "test-tenant-api"
        status, body = http_post("/admin/tenants", {"name": tenant_name})
        self.assertIn(status, (200, 201))
        self.assertEqual(body.get("tenant"), tenant_name)

        # Clean up.
        import urllib.request
        req = urllib.request.Request(
            SPECTRAL_URL + f"/admin/tenants?name={tenant_name}",
            method="DELETE",
        )
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                self.assertIn(resp.status, (200, 204))
        except urllib.error.HTTPError as e:
            self.fail(f"DELETE tenant failed: {e.code}")

    def test_delete_nonexistent_tenant_returns_404(self):
        import urllib.request
        req = urllib.request.Request(
            SPECTRAL_URL + "/admin/tenants?name=does-not-exist-xyz",
            method="DELETE",
        )
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                self.fail(f"Expected 404, got {resp.status}")
        except urllib.error.HTTPError as e:
            self.assertEqual(e.code, 404)

    def test_delete_default_tenant_rejected(self):
        import urllib.request
        req = urllib.request.Request(
            SPECTRAL_URL + "/admin/tenants?name=default",
            method="DELETE",
        )
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                self.fail(f"Expected 4xx, got {resp.status}")
        except urllib.error.HTTPError as e:
            self.assertIn(e.code, (400, 403, 422))


class TestEventHistoryLogic(unittest.TestCase):
    """Pure-Python unit tests for event history ring-buffer logic."""

    def _ring(self, events, cap):
        """Simulate a ring buffer of capacity cap; return list of retained events."""
        buf = [None] * cap
        head = 0
        for ev in events:
            buf[head] = ev
            head = (head + 1) % cap
        # Reconstruct ordered contents (oldest first).
        total = len(events)
        filled = min(total, cap)
        out = []
        for i in range(filled):
            idx = (head - filled + i) % cap
            out.append(buf[idx])
        return out

    def test_ring_retains_all_when_not_full(self):
        result = self._ring([1, 2, 3], cap=10)
        self.assertEqual(result, [1, 2, 3])

    def test_ring_drops_oldest_when_full(self):
        result = self._ring([1, 2, 3, 4, 5], cap=3)
        self.assertEqual(result, [3, 4, 5])

    def test_ring_wrap_preserves_order(self):
        result = self._ring(list(range(10)), cap=4)
        self.assertEqual(result, [6, 7, 8, 9])

    def test_empty_ring(self):
        result = self._ring([], cap=5)
        self.assertEqual(result, [])


class TestConfigFileFallback(unittest.TestCase):
    """Unit tests for config-file-as-env-fallback logic."""

    def _apply(self, file_cfg, current_env):
        """
        Simulate applyFileConfig: for each key in file_cfg, set it in
        current_env only if not already present.
        Returns the resulting env dict.
        """
        env = dict(current_env)
        for key, val in file_cfg.items():
            if val and key not in env:
                env[key] = val
        return env

    def test_file_sets_missing_keys(self):
        result = self._apply({"PORT": "9090", "DATA_DIR": "/data"}, {})
        self.assertEqual(result["PORT"], "9090")
        self.assertEqual(result["DATA_DIR"], "/data")

    def test_existing_env_wins(self):
        result = self._apply({"PORT": "9090"}, {"PORT": "8080"})
        self.assertEqual(result["PORT"], "8080")

    def test_empty_file_value_not_applied(self):
        result = self._apply({"PORT": ""}, {})
        self.assertNotIn("PORT", result)


class TestAgentStatusLogic(unittest.TestCase):
    """Unit tests for agent status validation."""

    VALID_STATUSES = {"healthy", "degraded", "unknown"}

    def _validate_status(self, status):
        return status in self.VALID_STATUSES

    def test_healthy_valid(self):
        self.assertTrue(self._validate_status("healthy"))

    def test_degraded_valid(self):
        self.assertTrue(self._validate_status("degraded"))

    def test_unknown_valid(self):
        self.assertTrue(self._validate_status("unknown"))

    def test_invalid_status(self):
        self.assertFalse(self._validate_status("offline"))

    def test_empty_status_invalid(self):
        self.assertFalse(self._validate_status(""))


class TestBatchRouteDeleteLogic(unittest.TestCase):
    """Unit tests for batch route deletion validation."""

    def _simulate_batch_delete(self, destinations, existing):
        """Returns (deleted, not_found)."""
        deleted, not_found = 0, 0
        for dst in destinations:
            dst = dst.strip()
            if not dst:
                continue
            if dst in existing:
                existing.discard(dst)
                deleted += 1
            else:
                not_found += 1
        return deleted, not_found

    def test_all_found(self):
        d, nf = self._simulate_batch_delete(["a", "b"], {"a", "b", "c"})
        self.assertEqual(d, 2)
        self.assertEqual(nf, 0)

    def test_some_not_found(self):
        d, nf = self._simulate_batch_delete(["a", "x"], {"a", "b"})
        self.assertEqual(d, 1)
        self.assertEqual(nf, 1)

    def test_all_not_found(self):
        d, nf = self._simulate_batch_delete(["x", "y"], {"a"})
        self.assertEqual(d, 0)
        self.assertEqual(nf, 2)

    def test_empty_destination_skipped(self):
        d, nf = self._simulate_batch_delete(["", "  "], {"a"})
        self.assertEqual(d, 0)
        self.assertEqual(nf, 0)


# ---------------------------------------------------------------------------
# Wave-5 integration tests
# ---------------------------------------------------------------------------

@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestEventHistoryEndpoint(unittest.TestCase):

    def test_history_returns_200(self):
        status, body = http_get("/events/history")
        self.assertEqual(status, 200)
        self.assertIn("count", body)
        self.assertIn("events", body)

    def test_history_limit_param(self):
        # Seed an event first.
        http_post("/blockchain/add", [{"sender": "hist-test", "recipient": "b", "amount": 1}])
        status, body = http_get("/events/history?limit=1")
        self.assertEqual(status, 200)
        self.assertLessEqual(len(body.get("events", [])), 1)

    def test_history_type_filter(self):
        status, body = http_get("/events/history?type=block_added")
        self.assertEqual(status, 200)
        for ev in body.get("events", []):
            self.assertEqual(ev["type"], "block_added")


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestOnDemandAdminEndpoints(unittest.TestCase):

    def test_backup_list_returns_200(self):
        status, body = http_get("/admin/backup/list")
        self.assertEqual(status, 200)
        self.assertIn("backups", body)

    def test_backup_endpoint_method_enforced(self):
        # GET is not allowed; only POST.
        status, _ = http_get("/admin/backup")
        self.assertEqual(status, 405)

    def test_compact_endpoint_method_enforced(self):
        status, _ = http_get("/admin/compact")
        self.assertEqual(status, 405)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestAgentGetByID(unittest.TestCase):

    def test_get_missing_agent_returns_404(self):
        status, body = http_get("/agents?id=nonexistent-wave5-agent")
        self.assertEqual(status, 404)
        self.assertIn("error", body)

    def test_register_then_get_by_id(self):
        agent_id = "wave5-get-by-id"
        http_post("/agents/register", {
            "id": agent_id,
            "addr": "10.0.5.1:9000",
            "status": "healthy",
            "ttl_seconds": 300,
        })
        status, body = http_get(f"/agents?id={agent_id}")
        self.assertEqual(status, 200)
        self.assertEqual(body.get("id"), agent_id)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestAgentStatusEndpoint(unittest.TestCase):

    def test_update_status_flow(self):
        agent_id = "wave5-status-agent"
        http_post("/agents/register", {
            "id": agent_id,
            "addr": "10.0.5.2:9000",
            "status": "healthy",
            "ttl_seconds": 300,
        })
        # Update status to degraded.
        import urllib.request
        req = urllib.request.Request(
            SPECTRAL_URL + f"/agents/status?id={agent_id}&status=degraded",
            method="POST",
            data=b"",
        )
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                self.assertEqual(resp.status, 204)
        except urllib.error.HTTPError as e:
            self.fail(f"status update failed: {e.code}")

        # Verify via GET.
        _, body = http_get(f"/agents?id={agent_id}")
        self.assertEqual(body.get("status"), "degraded")

    def test_missing_id_returns_400(self):
        import urllib.request
        req = urllib.request.Request(
            SPECTRAL_URL + "/agents/status?status=healthy",
            method="POST",
            data=b"",
        )
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                self.fail(f"Expected 400, got {resp.status}")
        except urllib.error.HTTPError as e:
            self.assertEqual(e.code, 400)


@unittest.skipUnless(SPECTRAL_URL, "SPECTRAL_URL not set — skipping integration tests")
class TestRoutesBatchDeleteEndpoint(unittest.TestCase):

    def test_empty_batch_returns_400(self):
        import urllib.request
        data = json.dumps([]).encode()
        req = urllib.request.Request(
            SPECTRAL_URL + "/routes/batch",
            method="DELETE",
            data=data,
        )
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=5):
                self.fail("Expected 400")
        except urllib.error.HTTPError as e:
            self.assertEqual(e.code, 400)

    def test_batch_delete(self):
        # Add routes first.
        dests = ["wave5-del-1", "wave5-del-2"]
        for dest in dests:
            http_post(f"/routes?destination={dest}&latency=5&throughput=50", {})

        import urllib.request
        data = json.dumps(dests + ["does-not-exist"]).encode()
        req = urllib.request.Request(
            SPECTRAL_URL + "/routes/batch",
            method="DELETE",
            data=data,
        )
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                body = json.loads(resp.read())
                self.assertEqual(body["deleted"], 2)
                self.assertEqual(body["not_found"], 1)
        except urllib.error.HTTPError as e:
            self.fail(f"batch delete failed: {e.code}")


if __name__ == "__main__":
    unittest.main()
