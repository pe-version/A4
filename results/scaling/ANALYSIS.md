# Horizontal Scaling Experiment

## Setup

- **Service under test:** Go sensor service (behind Nginx round-robin LB)
- **Tool:** Apache Bench (`ab`)
- **Parameters:** 1,000 requests, 50 concurrent connections
- **Infrastructure:** Docker Compose on local machine, shared Postgres database

## Session/State Strategy

The Go services are **stateless at the HTTP layer** — all state lives in a shared Postgres database. No session affinity, sticky sessions, or shared cache is needed. Any replica can handle any request equivalently because there is no in-memory session state. Nginx uses simple round-robin distribution.

## Results Summary

### GET /sensors (list all — read throughput)

| Metric | 1 Replica | 3 Replicas | Change |
|--------|-----------|------------|--------|
| Requests/sec | 58.06 | 93.53 | **+61%** |
| Mean latency | 861 ms | 535 ms | **-38%** |
| p50 latency | 799 ms | 406 ms | **-49%** |
| p90 latency | 1,598 ms | 1,002 ms | **-37%** |
| p99 latency | 2,416 ms | 1,600 ms | **-34%** |
| Max latency | 3,609 ms | 2,199 ms | **-39%** |

### GET /sensors/sensor-001 (single resource — point read latency)

| Metric | 1 Replica | 3 Replicas | Change |
|--------|-----------|------------|--------|
| Requests/sec | 71.69 | 93.06 | **+30%** |
| Mean latency | 697 ms | 537 ms | **-23%** |
| p50 latency | 600 ms | 589 ms | **-2%** |
| p90 latency | 1,197 ms | 999 ms | **-17%** |
| p99 latency | 2,011 ms | 1,401 ms | **-30%** |
| Max latency | 2,396 ms | 1,605 ms | **-33%** |

### POST /sensors (write throughput)

| Metric | 1 Replica | 3 Replicas (original) | 3 Replicas (post-fix) |
|--------|-----------|-----------------------|-----------------------|
| Requests/sec | 54.60 | 77.34 | 76.06 |
| Mean latency | 916 ms | 647 ms | 657 ms |
| p50 latency | 808 ms | 600 ms | 601 ms |
| Failure rate | **95.3%** | **94.5%** | **0.7%** ✓ |

## Analysis

### Read Performance

Scaling from 1 to 3 replicas improved read throughput by 30–61% and reduced latency across all percentiles. The improvement is not a perfect 3x because the bottleneck is shared: all replicas contend on the same Postgres connection pool. The list-all endpoint (`GET /sensors`) benefited more than the point-read (`GET /sensors/sensor-001`) because the larger response payload amplifies the per-replica processing cost that parallelism can absorb.

### Write Performance

The original scaling test exposed a 94–95% write failure rate across both 1-replica and 3-replica configurations. Investigation traced the failures to the ID generation strategy in the repositories: each `Create` ran `SELECT MAX(CAST(SUBSTR(id, 8) AS INTEGER)) FROM sensors` inside a transaction, computed `MAX + 1`, then inserted the new row. Under concurrent load, two (or more) transactions would both see the same MAX, compute the same next ID, and one would win the primary key race — every other concurrent write got a duplicate-key violation.

**Fix implemented:** Replaced the read-modify-write pattern with a Postgres `SEQUENCE`:

```sql
CREATE SEQUENCE IF NOT EXISTS sensor_id_seq;
-- at startup, after seed:
SELECT setval('sensor_id_seq',
    COALESCE((SELECT MAX(CAST(SUBSTR(id, 8) AS INTEGER))
              FROM sensors WHERE id LIKE 'sensor-%'), 0));
```

```go
// In Create():
var nextNum int64
r.db.QueryRow("SELECT nextval('sensor_id_seq')").Scan(&nextNum)
```

`nextval()` is atomic and lock-free; concurrent callers get unique values with no race. The `setval` at startup handles the seed-collision case: after seeding `sensor-001` through `sensor-006` from JSON, the sequence is advanced past 6 so the first `nextval()` returns 7. The same pattern is applied to `rule_id_seq` and `alert_id_seq` in the alert service.

**Verified result:** Write failure rate under the same 1,000-request / 50-concurrent load test dropped from **94.5% to 0.7%**. Throughput and latency were effectively unchanged (the writes that previously failed fast now complete, but the bottleneck remains shared Postgres I/O — the *ceiling* hasn't moved, only the correctness has).

This is a good illustration of a principle the scaling analysis revealed: a "scaling" bottleneck often turns out to be a correctness bug under concurrency, not a resource bottleneck. The original test at 3 replicas was reporting 77 req/s at 94.5% failure — i.e., only ~4 successful writes/sec. The post-fix test reports 76 req/s at 0.7% failure — ~75 successful writes/sec, an **18x real improvement** in successful write throughput that was entirely masked by the race condition.

### Why Not 3x Improvement?

The shared Postgres database is the limiting factor. With 3 replicas, the Go HTTP layer has 3x the goroutine capacity, but all database queries still funnel through a single Postgres instance. True linear scaling would require either:
- **Read replicas** — Postgres streaming replication with reads distributed across replicas
- **Connection pooling** — PgBouncer in front of Postgres to manage connection saturation
- **Database sharding** — splitting data across multiple Postgres instances

For this experiment, the ~50% improvement demonstrates that the HTTP layer was indeed a bottleneck that horizontal scaling alleviated, even with a shared database.

## Evidence of Load Distribution

Docker Compose logs confirm all 3 replicas handled requests during the test:

```
go-service-1  | INFO Request completed method=POST path=/sensors status=400 duration_ms=593
go-service-2  | INFO Request completed method=POST path=/sensors status=400 duration_ms=588
go-service-3  | INFO Request completed method=POST path=/sensors status=201 duration_ms=591
```

Each replica has a unique container name and correlation ID in its structured logs.

## Traffic Flow

See `diagrams/scaling-traffic-flow.mmd` for the Mermaid diagram. Traffic flows:

```
Client → Nginx LB (:8080) → round-robin → go-service-{1,2,3} → Postgres (sensor-db)
```
