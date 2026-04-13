#!/usr/bin/env bash
# scaling_test.sh — Horizontal scaling experiment for A4.
#
# Runs Apache Bench against the Go sensor service (through Nginx LB)
# and captures throughput/latency results.
#
# Usage:
#   ./scripts/scaling_test.sh <label>
#
# Example:
#   ./scripts/scaling_test.sh 1-replica
#   ./scripts/scaling_test.sh 3-replicas

set -euo pipefail

LABEL="${1:?Usage: $0 <label>}"
TOKEN="${API_TOKEN:-test-secret}"
BASE_URL="http://localhost:8080"
RESULTS_DIR="results/scaling"

TOTAL_REQUESTS=1000
CONCURRENCY=50

mkdir -p "$RESULTS_DIR"

echo "╔═══════════════════════════════════════════════════╗"
echo "║  Scaling Experiment: $LABEL"
echo "║  Requests: $TOTAL_REQUESTS  Concurrency: $CONCURRENCY"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

# ── 1. Verify service is up ──────────────────────────────────────────────────

echo "=== Preflight check ==="
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/health")
if [[ "$STATUS" != "200" ]]; then
  echo "ERROR: health check failed (HTTP $STATUS)"
  exit 1
fi
echo "Health check OK"
echo ""

# ── 2. GET /sensors (read throughput) ────────────────────────────────────────

echo "=== GET /sensors (read throughput) ==="
ab -n "$TOTAL_REQUESTS" -c "$CONCURRENCY" \
   -H "Authorization: Bearer $TOKEN" \
   "$BASE_URL/sensors" \
   2>&1 | tee "$RESULTS_DIR/get-sensors-$LABEL.txt"
echo ""

# ── 3. GET /sensors/:id (single resource latency) ───────────────────────────

echo "=== GET /sensors/sensor-001 (single resource latency) ==="
ab -n "$TOTAL_REQUESTS" -c "$CONCURRENCY" \
   -H "Authorization: Bearer $TOKEN" \
   "$BASE_URL/sensors/sensor-001" \
   2>&1 | tee "$RESULTS_DIR/get-sensor-by-id-$LABEL.txt"
echo ""

# ── 4. POST /sensors (write throughput) ──────────────────────────────────────

echo "=== POST /sensors (write throughput) ==="

TMPFILE=$(mktemp)
cat > "$TMPFILE" <<'ENDJSON'
{"name":"Load Test Sensor","type":"temperature","location":"test_room","value":72.5,"unit":"fahrenheit","status":"active"}
ENDJSON

ab -n "$TOTAL_REQUESTS" -c "$CONCURRENCY" \
   -H "Authorization: Bearer $TOKEN" \
   -H "Content-Type: application/json" \
   -p "$TMPFILE" \
   "$BASE_URL/sensors" \
   2>&1 | tee "$RESULTS_DIR/post-sensors-$LABEL.txt"

rm -f "$TMPFILE"
echo ""

echo "Results saved to $RESULTS_DIR/*-$LABEL.txt"
