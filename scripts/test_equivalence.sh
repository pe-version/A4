#!/usr/bin/env bash
# Verifies that blocking and reactive pipelines produce identical alert outputs
# for the same input dataset.
set -euo pipefail

API_TOKEN="${API_TOKEN:?API_TOKEN is required}"
STACK="${1:-python}"  # python or go

if [ "$STACK" = "python" ]; then
  SENSOR_PORT=8000; ALERT_PORT=8002
else
  SENSOR_PORT=8080; ALERT_PORT=8081
fi

AUTH="Authorization: Bearer $API_TOKEN"
SENSOR_URL="http://localhost:$SENSOR_PORT"
ALERT_URL="http://localhost:$ALERT_PORT"

echo "=== Deterministic Equivalence Test ($STACK stack) ==="

run_phase() {
  local mode=$1
  local output_file="/tmp/equivalence_${STACK}_${mode}.json"

  echo ""
  echo "--- Phase: $mode ---"

  # Restart in the target mode
  PIPELINE_MODE=$mode WORKER_COUNT=4 docker compose up -d --build --quiet-pull 2>/dev/null
  sleep 10  # wait for services to stabilize

  # Clear alerts by deleting existing ones
  local existing
  existing=$(curl -sf -H "$AUTH" "$ALERT_URL/alerts" | python3 -c "import sys,json; [print(a['id']) for a in json.load(sys.stdin).get('alerts',[])]" 2>/dev/null || true)
  for id in $existing; do
    curl -sf -X DELETE -H "$AUTH" "$ALERT_URL/alerts/$id" >/dev/null 2>&1 || true
  done

  # Send 5 identical sensor updates above threshold
  for i in 1 2 3 4 5; do
    curl -sf -X PUT -H "$AUTH" -H "Content-Type: application/json" \
      -d "{\"value\": $((80 + i)).0}" \
      "$SENSOR_URL/sensors/sensor-007" >/dev/null
    sleep 0.5
  done

  sleep 5  # wait for all events to process

  # Collect triggered alerts (sorted by rule_id for stable comparison)
  curl -sf -H "$AUTH" "$ALERT_URL/alerts" | \
    python3 -c "
import sys, json
data = json.load(sys.stdin)
alerts = data.get('alerts', [])
# Extract only deterministic fields
result = []
for a in alerts:
    result.append({
        'rule_id': a['rule_id'],
        'sensor_id': a['sensor_id'],
        'threshold': a['threshold'],
        'status': a['status'],
    })
result.sort(key=lambda x: x['rule_id'])
print(json.dumps({'count': len(alerts), 'alerts': result}, indent=2))
" > "$output_file"

  echo "Alerts captured: $(cat "$output_file" | python3 -c 'import sys,json; print(json.load(sys.stdin)["count"])')"
  cat "$output_file"
}

run_phase "blocking"
run_phase "async"

echo ""
echo "=== Comparing outputs ==="

BLOCKING="/tmp/equivalence_${STACK}_blocking.json"
ASYNC="/tmp/equivalence_${STACK}_async.json"

# Compare alert counts
B_COUNT=$(python3 -c "import json; print(json.load(open('$BLOCKING'))['count'])")
A_COUNT=$(python3 -c "import json; print(json.load(open('$ASYNC'))['count'])")

if [ "$B_COUNT" -eq "$A_COUNT" ] && [ "$B_COUNT" -gt 0 ]; then
  echo "PASS: Both modes produced $B_COUNT alerts"
else
  echo "FAIL: Blocking produced $B_COUNT alerts, Reactive produced $A_COUNT alerts"
  exit 1
fi

# Compare alert content (rule_id, sensor_id, threshold)
if diff <(python3 -c "import json; [print(a['rule_id'],a['sensor_id'],a['threshold']) for a in json.load(open('$BLOCKING'))['alerts']]") \
        <(python3 -c "import json; [print(a['rule_id'],a['sensor_id'],a['threshold']) for a in json.load(open('$ASYNC'))['alerts']]") >/dev/null; then
  echo "PASS: Alert content is identical across both modes"
else
  echo "FAIL: Alert content differs between modes"
  diff <(python3 -c "import json; [print(a['rule_id'],a['sensor_id'],a['threshold']) for a in json.load(open('$BLOCKING'))['alerts']]") \
       <(python3 -c "import json; [print(a['rule_id'],a['sensor_id'],a['threshold']) for a in json.load(open('$ASYNC'))['alerts']]")
  exit 1
fi

echo ""
echo "=== RESULT: Deterministic equivalence confirmed ==="
