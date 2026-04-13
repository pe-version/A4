# Observability Deep Dive

## Observability Infrastructure

### Structured Logs with Correlation IDs

Every HTTP request is assigned a unique `correlation_id` (UUID) by the logging middleware. If the client provides an `X-Correlation-ID` header, that value is used instead, allowing end-to-end request tracing from the caller through the system. Each log line includes the correlation ID, HTTP method, path, status code, and duration:

```
go-service-1  | INFO Request completed correlation_id=TRACE-DEMO-001 method=PUT path=/sensors/sensor-001 status=200 duration_ms=15
```

### Distributed Tracing Across Services

When a sensor is updated, the Go sensor service generates a `trace_id` and embeds it in the RabbitMQ message payload. The Go alert service extracts this trace ID when consuming the message and includes it in all subsequent log entries (rule evaluation, alert triggering). This provides a causal link across the async boundary:

**Step 1 — Sensor service publishes event:**
```
go-service-1  | INFO Sensor updated, publishing event sensor_id=sensor-001 trace_id=f5066d07-1c8b-401d-b5f4-6219da618876
go-service-1  | INFO Published sensor.updated event sensor_id=sensor-001 value=95 trace_id=f5066d07-1c8b-401d-b5f4-6219da618876
```

**Step 2 — Alert service consumes and evaluates:**
```
go-alert-service-1  | INFO Received sensor.updated event sensor_id=sensor-001 value=95 trace_id=f5066d07-1c8b-401d-b5f4-6219da618876
go-alert-service-1  | INFO Alert triggered alert_id=alert-004 rule_id=rule-001 trace_id=f5066d07-1c8b-401d-b5f4-6219da618876 message="Sensor sensor-001 value 95.00 gt threshold 80.00 (rule: Living Room Temperature High)"
go-alert-service-1  | INFO Alert triggered alert_id=alert-005 rule_id=rule-004 trace_id=f5066d07-1c8b-401d-b5f4-6219da618876 message="Sensor sensor-001 value 95.00 gt threshold 90.00 (rule: Verify Smoke Test Rule Go)"
```

The same `trace_id` (`f5066d07...`) appears in both services, linking the sensor update to the resulting alerts across the RabbitMQ async boundary.

### Metrics

The Go alert service exposes Prometheus-format metrics on `:9090/metrics`:

```
events_received_total 1        — events consumed from RabbitMQ
events_processed_total 1       — events that completed evaluation
alerts_triggered_total 2       — threshold crossings that generated alerts
event_processing_avg_microseconds 32002.0  — average evaluation latency
pipeline_info{mode="blocking",worker_count="4"} 1
```

These counters are monotonically increasing and safe for concurrent access (atomic operations). They allow monitoring the health of the async pipeline without parsing logs.

---

## Debugging Story: Database Outage

### The Incident

During operation, the Go sensor service began returning HTTP 500 errors for all data endpoints. The health endpoint continued responding HTTP 200.

### Detection

The first signal was client-facing: `GET /sensors/sensor-001` returned:

```json
{"detail": "Failed to retrieve sensor"}
```

with HTTP 500 and a response time of 94ms (vs. the normal ~5ms). Subsequent requests also failed but responded faster (5ms) — the first request paid the TCP connection timeout to discover the database was unreachable, while later requests failed immediately from the connection pool.

### Triage

The health endpoint was checked and responded normally:

```json
{"status": "ok", "service": "go"}
```

This immediately ruled out a full service crash, network partition to the LB, or application-level panic. The failure was isolated to data-dependent operations.

### Diagnosis via Structured Logs

Filtering the Go service logs by time window revealed the transition from healthy to unhealthy:

```
correlation_id=9a6c9938... method=GET path=/sensors/sensor-001 status=200 duration_ms=0   ← last healthy request
correlation_id=782ffbb3... method=GET path=/sensors/sensor-001 status=500 duration_ms=90  ← first failure (90ms — TCP timeout)
correlation_id=ac4e2ffa... method=GET path=/sensors/sensor-001 status=500 duration_ms=3   ← subsequent failures (fast, pool already knows)
correlation_id=8630097e... method=GET path=/sensors/sensor-001 status=500 duration_ms=3
```

The 90ms → 3ms pattern confirmed a connection-level failure: the first request waited for the TCP handshake to time out, and the connection pool cached the failure state for subsequent requests.

A write attempt produced an explicit error log:

```
ERROR Failed to update sensor error="dial tcp: lookup sensor-db on 127.0.0.11:53: no such host" sensor_id=sensor-001
```

This confirmed the root cause: the `sensor-db` container was unreachable (DNS resolution failed), meaning Postgres was down, not just slow.

### Impact Assessment

- **Read endpoints:** HTTP 500 — all reads failed
- **Write endpoints:** HTTP 503 with sanitized error — writes failed gracefully
- **Health endpoint:** HTTP 200 — unaffected (no DB dependency)
- **Async pipeline:** No new events processed (sensor updates couldn't be written, so no RabbitMQ publish occurred)
- **Alert service metrics:** Unchanged — `events_received_total` held steady at its pre-outage value, confirming no new events entered the pipeline

### Recovery

Restarting the Postgres container (`docker start a4-sensor-db-1`) restored service within seconds. The Go service's `database/sql` connection pool automatically reconnected — no service restart was required. The log transition:

```
correlation_id=e58517c3... method=PUT path=/sensors/sensor-001 status=503 duration_ms=3    ← last failure
correlation_id=3ee7c93d... method=GET path=/sensors/sensor-001 status=200 duration_ms=7    ← first success (7ms — new connection)
correlation_id=fe9a3d19... method=GET path=/sensors/sensor-001 status=200 duration_ms=0    ← normal operation resumed
```

All data persisted through the outage — sensor values were unchanged after recovery.

### Root Cause

The `sensor-db` Postgres container was stopped, causing DNS resolution failure and TCP connection refusal for all Go service replicas sharing the `sensor-db` Docker network.

### What Telemetry Told Us (And What It Couldn't)

**What worked:**
- Structured logs with correlation IDs allowed precise sequencing of the failure onset
- The duration_ms field distinguished the initial TCP timeout (90ms) from cached pool failures (3ms), pointing to a connection-level issue
- The explicit ERROR log with the DNS resolution message confirmed the exact root cause
- The health endpoint remaining healthy allowed rapid triage (service alive, database dead)
- Alert service metrics being unchanged confirmed the blast radius was limited to the sensor service's data layer

**What was missing:**
- No alerting — the failure was detected by manual observation, not by an automated monitor watching for 500 rate spikes
- No readiness probe — the health endpoint reported "ok" while the service was unable to serve requests, which would mislead a load balancer in production
- No database-specific metrics (connection pool size, active/idle connections, error rate) — these would have given earlier warning of connection exhaustion under gradual degradation rather than abrupt failure
