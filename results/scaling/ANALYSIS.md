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

| Metric | 1 Replica | 3 Replicas | Change |
|--------|-----------|------------|--------|
| Requests/sec | 54.60 | 77.34 | **+42%** |
| Mean latency | 916 ms | 647 ms | **-29%** |
| p50 latency | 808 ms | 600 ms | **-26%** |
| Failure rate | 95.3% | 94.5% | Similar |

## Analysis

### Read Performance

Scaling from 1 to 3 replicas improved read throughput by 30–61% and reduced latency across all percentiles. The improvement is not a perfect 3x because the bottleneck is shared: all replicas contend on the same Postgres connection pool. The list-all endpoint (`GET /sensors`) benefited more than the point-read (`GET /sensors/sensor-001`) because the larger response payload amplifies the per-replica processing cost that parallelism can absorb.

### Write Performance

Write throughput improved by 42%, but the high failure rate (94–95%) persisted across both configurations. The failures are `400 Bad Request` responses caused by the sequential ID generation strategy: concurrent transactions race to compute `MAX(id) + 1`, and losers violate the primary key constraint. This is a **known limitation of the ID generation pattern**, not a scaling defect. It would be resolved by switching to UUIDs or Postgres sequences (`SERIAL` / `GENERATED ALWAYS AS IDENTITY`), which eliminate the read-modify-write race entirely. This is noted as a future improvement.

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
