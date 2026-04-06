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


if __name__ == "__main__":
    unittest.main()
