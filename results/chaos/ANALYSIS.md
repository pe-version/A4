# Failure & Chaos Testing

## Scenario 1: Kill a Service Replica

### Prediction

With 3 Go sensor service replicas behind Nginx round-robin, killing one replica should cause Nginx to detect the dead upstream on the first failed connection attempt and route subsequent requests to the surviving 2 replicas. Zero client-visible errors are expected because Nginx retries the next upstream on connection failure. Docker's `restart: unless-stopped` policy should automatically restart the killed container.

### Execution

```bash
docker kill a4-go-service-2
```

### What Actually Happened

**No client-visible errors** — all 10 requests returned HTTP 200. However, **one request took 14.3 seconds** (vs. the normal ~4ms) because it was routed to the dead replica's IP, and Nginx waited for the upstream connection timeout before retrying on a healthy replica.

Under sustained load (100 requests, 10 concurrent), throughput dropped from ~93 req/s (3 replicas) to ~10.8 req/s because Nginx continued routing 1/3 of requests to the dead IP, each incurring the full timeout penalty before failover.

Docker did **not** automatically restart the killed container (SIGKILL exit code 137). Manual `docker start` was required to bring it back. Once restarted, all requests returned to normal latency immediately.

### Key Findings

| Phase | Throughput | p50 Latency | Errors |
|-------|-----------|-------------|--------|
| Before kill (3 replicas) | ~93 req/s | 589 ms | 0% |
| After kill (2 replicas) | ~10.8 req/s | 16 ms* | 0% |
| After recovery (3 replicas) | Normal | ~4 ms | 0% |

*The p50 was fast because 2/3 of requests hit healthy replicas instantly, but p90 was 3,072 ms due to the timeout penalty on requests routed to the dead upstream.

### Improvement Implemented: Nginx Upstream Timeout Tuning

The default Nginx upstream timeout is 60 seconds, meaning requests routed to the dead replica wait far too long before failover. To mitigate this, the Nginx upstream configuration should be tuned with shorter timeouts and failure detection:

```nginx
proxy_connect_timeout 2s;
proxy_next_upstream error timeout;
proxy_next_upstream_tries 2;
```

This reduces the worst-case failover penalty from ~14 seconds to ~2 seconds per affected request.

---

## Scenario 2: Stop the Database

### Prediction

Stopping the Postgres `sensor-db` container should cause all data-dependent endpoints (GET/POST/PUT/DELETE) to return errors, while the health endpoint (`/health`) should continue responding since it does not query the database. The Go services should not crash — they should return error responses gracefully. When Postgres restarts, the Go services should reconnect automatically through the `database/sql` connection pool without requiring a service restart. Data should persist across the outage (Postgres uses a named Docker volume).

### Execution

```bash
docker stop a4-sensor-db-1
```

### What Actually Happened

**Data endpoints returned errors immediately:**
- `GET /sensors/sensor-001` → HTTP 500: `{"detail":"Failed to retrieve sensor"}`
- `POST /sensors` → HTTP 400: `{"detail":"dial tcp: lookup sensor-db on 127.0.0.11:53: no such host"}`

**Health endpoint continued responding:** `GET /health` → HTTP 200, confirming the service process itself was unaffected.

**After restarting Postgres** (`docker start a4-sensor-db-1`), data endpoints recovered within seconds — no Go service restart required. All data persisted across the outage; `sensor-001` was retrievable with its original values immediately after recovery.

### Key Findings

| Endpoint | During Outage | After Recovery |
|----------|--------------|----------------|
| GET /sensors/:id | HTTP 500 (5ms) | HTTP 200 (10-20ms) |
| POST /sensors | HTTP 400 (11ms) | HTTP 201 |
| GET /health | HTTP 200 (2ms) | HTTP 200 (2ms) |

### Issues Discovered

1. **Internal error leakage:** The POST endpoint returned the raw DNS resolution error (`dial tcp: lookup sensor-db on 127.0.0.11:53: no such host`) directly to the client. This exposes internal infrastructure details (hostnames, DNS resolver addresses) and should be replaced with a generic error message like `{"detail":"Service temporarily unavailable"}`.

2. **Inconsistent error codes:** Read failures correctly returned HTTP 500, but the write failure returned HTTP 400 (Bad Request). A database connectivity issue is not a client error — it should be HTTP 503 (Service Unavailable) to correctly signal a transient server-side failure that the client can retry.

3. **No readiness distinction:** The health endpoint reports "ok" even when the database is unreachable. In a production system, the health check should differentiate between liveness (process is running) and readiness (process can serve requests). Kubernetes-style `/healthz` vs `/readyz` endpoints would allow the load balancer to stop routing traffic to replicas that have lost their database connection.

### Improvement: Error Response Sanitization

The most actionable improvement is sanitizing error responses to prevent internal details from leaking to clients. Database connectivity errors should be caught and returned as:

```json
{"detail": "Service temporarily unavailable"}
```

with HTTP 503 status, regardless of the specific internal failure. This is both a security improvement (information disclosure) and a correctness improvement (proper HTTP semantics for transient failures).

---

## Scenario 3: Disconnect the Queue (RabbitMQ)

### Prediction

Stopping RabbitMQ should cause the sensor service's event publisher to fail when it tries to emit the next `sensor.updated` event. The HTTP write itself will still succeed because Postgres is independent of the message broker. The alert service consumer, blocked on a closed channel, should detect the connection loss and enter its retry/backoff loop. When RabbitMQ comes back online, the consumer should auto-reconnect without a service restart and the pipeline should resume. Events published during the outage will be lost — the publisher uses fire-and-forget semantics with no persistent outbox.

### Execution

```bash
docker stop a4-rabbitmq-1
# trigger a sensor update (expected: HTTP 200, but no event published)
# restart RabbitMQ
docker start a4-rabbitmq-1
# trigger another sensor update (expected: event published and received)
```

### What Actually Happened

**Before the outage:** A sensor update to `85.0` (crossing the `gt 80.0` threshold) produced the expected behavior — event published with trace ID `abdd58db`, received by the alert service, alert triggered.

**During the outage:**
- The HTTP write succeeded (HTTP 200) because Postgres was unaffected
- The publisher logged: `WARN Failed to publish sensor event, attempting reconnect error="Exception (504) Reason: channel/connection is not open"`
- The publisher's reconnect attempt also failed: `WARN RabbitMQ reconnect failed — event dropped sensor_id=sensor-001 error="dial tcp: lookup rabbitmq on 127.0.0.11:53: no such host"` — confirming the event for value `99.0` was lost
- The alert service consumer logged the connection loss twice during the outage: `ERROR Consumer connection lost — reconnecting in 5s` (first immediately, then 5s later, continuing on backoff)

**Recovery (autonomous):**
- When RabbitMQ came back, the alert consumer's backoff loop succeeded on its next attempt: `INFO Connected to RabbitMQ, waiting for sensor events`
- The next sensor update (value `88.0`, trace ID `8da05634`) was published and received normally — no service restart required

**Metrics evidence of the lost event:**

| Stage | events_received_total | alerts_triggered_total |
|-------|----------------------|----------------------|
| Before outage | 2 | 3 |
| During outage (after 99.0 update) | 2 | 3 |
| After recovery (after 88.0 update) | 3 | 4 |

The counters incremented by exactly one between "before outage" and "after recovery," despite two sensor updates being issued during that window. The update at `99.0` produced no event — the Prometheus counter confirms the data loss quantitatively.

### Key Findings

| Phase | Sensor write | Event published | Consumer status |
|-------|-------------|-----------------|-----------------|
| Before | HTTP 200 | ✓ delivered | connected |
| During | HTTP 200 | ✗ dropped | reconnecting every 5s |
| After | HTTP 200 | ✓ delivered | connected |

### Why This Matters

The system **self-healed** from a broker outage without any human intervention. The sensor service's publisher and the alert service's consumer both have reconnect logic (carried forward from A3) that works transparently across Docker Compose restarts of RabbitMQ. Operationally, this is exactly what you want for a transient broker failure.

### What the Test Exposed

1. **Silent data loss.** Events published during the outage are lost with only a WARN log. A production system would need a persistent outbox pattern — write the event to a local queue/table as part of the same transaction as the Postgres write, then a separate worker drains that outbox to RabbitMQ with retries. This provides at-least-once delivery across broker failures.
2. **No backpressure on the HTTP write.** The sensor PUT returned HTTP 200 even though the downstream pipeline failed. A caller has no way to know the alert pipeline didn't observe this update. An idempotency key and an explicit "event accepted" vs "event delivered" distinction would clarify this in production.
3. **Reconnect logging frequency.** The consumer logs every 5-second retry attempt. During a long outage this could flood logs. Exponential backoff with capped retry interval (e.g., 5s → 30s → 5min → 5min) would reduce noise.

The core point for this assignment: **the recovery did not require code changes or operator intervention.** The existing reconnect logic (A3-era) combined with Docker Compose's orchestration was sufficient to restore full functionality.
