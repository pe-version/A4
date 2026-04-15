# A4 – Scaling, Hardening, and Operating Microservices

## Service Definition

This assignment evolves the system built over A1–A3 into a production-oriented architecture with horizontal scaling, failure resilience, security hardening, and operational observability.

**Domain:** IoT Smart Home Sensors + Alert Rules
**Services:** Sensor service (Python + Go) and Alert service (Python + Go)
**Persistence:** Postgres (Go services), SQLite (Python services, A3 baseline)
**Messaging:** RabbitMQ (fanout exchange)
**Resilience:** Circuit breaker + HTTP retry with exponential backoff
**Authentication:** Bearer Token

## Architecture

![Architecture Diagram](diagrams/architecture.png)

```mermaid
flowchart TB
    Client["HTTP Client\n(curl, Postman, etc.)"]

    subgraph Docker["Docker Compose Network"]
        RabbitMQ["RabbitMQ\n:5672 / :15672"]

        subgraph SensorServices["Sensor Services"]
            PythonSvc["Python Sensor\nFastAPI · :8000"]
            GoSvc["Go Sensor\nGin · :8080"]
        end

        subgraph AlertServices["Alert Services"]
            PythonAlert["Python Alert\nFastAPI · :8002"]
            GoAlert["Go Alert\nGin · :8081"]
        end

        subgraph Storage["Data Storage"]
            PDB[("sensors-python.db")]
            GDB[("sensors-go.db")]
            PAB[("alerts-python.db")]
            GAB[("alerts-go.db")]
        end
    end

    Client -->|"Bearer token"| PythonSvc
    Client -->|"Bearer token"| GoSvc
    Client -->|"Bearer token"| PythonAlert
    Client -->|"Bearer token"| GoAlert

    GoSvc -->|"PUT /sensors/:id\npublishes sensor.updated"| RabbitMQ
    PythonSvc -->|"PUT /sensors/:id\npublishes sensor.updated"| RabbitMQ
    RabbitMQ -->|"sensor.updated events"| GoAlert
    RabbitMQ -->|"sensor.updated events"| PythonAlert

    GoAlert -->|"GET /sensors/:id\n(circuit breaker)"| GoSvc
    PythonAlert -->|"GET /sensors/:id\n(retries + timeout)"| PythonSvc

    PythonSvc --> PDB
    GoSvc --> GDB
    PythonAlert --> PAB
    GoAlert --> GAB
```

## Sequence Diagram: Sensor Update → Alert Evaluation

This is the critical async flow. A client updates a sensor value; the sensor service publishes an event; the alert service evaluates it against active rules.

![Async Flow Sequence Diagram](diagrams/sequence-async-flow.png)

```mermaid
sequenceDiagram
    actor Client
    participant GoSvc as Go Sensor Service
    participant RMQ as RabbitMQ
    participant GoAlert as Go Alert Service
    participant AlertDB as alerts-go.db

    Client->>+GoSvc: PUT /sensors/sensor-001\nAuthorization: Bearer token
    GoSvc->>GoSvc: Update sensor in DB
    GoSvc-->>-Client: 200 OK {sensor}

    GoSvc-)RMQ: Publish sensor.updated\n{sensor_id, value, type, unit}

    Note over GoSvc,RMQ: Fire-and-forget (goroutine)\nHTTP response already sent

    RMQ-)GoAlert: Deliver sensor.updated event

    GoAlert->>GoAlert: Load active rules for sensor-001
    loop For each matching rule
        GoAlert->>GoAlert: Evaluate value OP threshold
        alt Threshold crossed
            GoAlert->>AlertDB: INSERT triggered_alert
            GoAlert->>GoAlert: Log alert triggered
        end
    end
```

## Resilience: Circuit Breaker Flow

When the alert service creates a rule, it validates the sensor exists via HTTP with circuit breaker protection:

![Circuit Breaker Sequence Diagram](diagrams/sequence-circuit-breaker.png)

```mermaid
sequenceDiagram
    actor Client
    participant GoAlert as Go Alert Service
    participant CB as Circuit Breaker
    participant GoSvc as Go Sensor Service

    Client->>+GoAlert: POST /rules {sensor_id: "sensor-001"}
    GoAlert->>CB: Execute GET /sensors/sensor-001

    alt Sensor service healthy
        CB->>+GoSvc: GET /sensors/sensor-001
        GoSvc-->>-CB: 200 OK {sensor}
        CB-->>GoAlert: sensor found
        GoAlert->>GoAlert: Create rule in DB
        GoAlert-->>-Client: 201 Created {rule}
    else Sensor not found (404)
        CB->>+GoSvc: GET /sensors/sensor-001
        GoSvc-->>-CB: 404 Not Found
        CB-->>GoAlert: not found error
        GoAlert-->>Client: 400 Bad Request "Sensor not found"
    else Sensor service down → circuit trips OPEN
        loop Retry with backoff (up to 3 attempts, 1s / 2s delays)
            CB->>GoSvc: GET /sensors/sensor-001
            GoSvc--xCB: connection refused (timeout)
            Note over CB: failure count +1
        end
        Note over CB: ConsecutiveFailures >= fail_max → OPEN
        CB-->>GoAlert: CircuitBreakerError (no further network calls)
        GoAlert->>GoAlert: Create rule with warning
        GoAlert-->>Client: 201 Created {rule, warning}
    end
```

## Project Structure

```
A4/
├── README.md
├── architecture-review.md           # A4 architecture review document
├── .env.example                     # Secrets template (copy to .env)
├── docker-compose.yml               # All services + Postgres + Nginx LBs + RabbitMQ
├── openapi.yaml
├── nginx/
│   ├── sensor.conf                  # Nginx LB for Go sensor replicas
│   └── alert.conf                   # Nginx LB for Go alert replicas
├── adrs/
│   ├── ADR-001-sync-vs-async.md
│   ├── ADR-002-rabbitmq-selection.md
│   └── ADR-003-circuit-breaker.md
├── data/
│   ├── sensors.json
│   └── alert_rules.json
├── scripts/
│   ├── verify.sh                    # End-to-end smoke test
│   ├── scaling_test.sh              # Horizontal scaling load test
│   └── load_test.sh                 # A3 pipeline load test
├── results/
│   ├── scaling/                     # 1-replica vs 3-replica comparison
│   ├── chaos/                       # Kill replica + stop DB experiments
│   ├── security/                    # Network isolation, secrets, headers
│   └── observability/               # Distributed trace + debugging story
├── diagrams/
├── python-service/                  # Sensor service (Python/FastAPI, SQLite, A3 baseline)
├── go-service/                      # Sensor service (Go/Gin, Postgres, horizontally scaled)
├── python-alert-service/            # Alert service (Python/FastAPI, SQLite, A3 baseline)
└── go-alert-service/                # Alert service (Go/Gin, Postgres, horizontally scaled)
```

## API Endpoints

### Sensor Services (Python :8000 / Go :8080)

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/health` | No | Health check |
| GET | `/sensors` | Yes | List all sensors |
| GET | `/sensors/{id}` | Yes | Get sensor by ID |
| POST | `/sensors` | Yes | Create sensor |
| PUT | `/sensors/{id}` | Yes | Update sensor (**publishes sensor.updated event**) |
| DELETE | `/sensors/{id}` | Yes | Delete sensor |

### Alert Services (Python :8002 / Go :8081)

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/health` | No | Health check |
| GET | `/rules` | Yes | List all alert rules |
| GET | `/rules/{id}` | Yes | Get alert rule |
| POST | `/rules` | Yes | Create rule (validates sensor via circuit breaker) |
| PUT | `/rules/{id}` | Yes | Update rule |
| DELETE | `/rules/{id}` | Yes | Delete rule |
| GET | `/alerts` | Yes | List triggered alerts |
| GET | `/alerts/{id}` | Yes | Get triggered alert |
| PUT | `/alerts/{id}` | Yes | Update alert status |

## Running the Services

### Prerequisites

- Docker and Docker Compose
- `API_TOKEN` environment variable set

### Quick Start

```bash
cp .env.example .env   # Edit .env with your credentials
cd A4
docker compose up --build
```

Services start in order: Postgres + RabbitMQ → Sensor services → Nginx LBs → Alert services (enforced by `depends_on` health checks).

To run with multiple Go replicas:

```bash
SENSOR_REPLICAS=3 ALERT_REPLICAS=3 docker compose up --build
```

### Stop

```bash
docker compose down
```

## Running Tests

### Go Tests

Tests require a running Postgres instance. Set `TEST_DATABASE_DSN` or use the default (`postgres://iot_user:iot_secret@localhost:5432/sensors_test?sslmode=disable`).

```bash
cd go-service && go test ./tests/ -v
cd go-alert-service && go test ./tests/ -v
```

The evaluator unit test (pure logic, no DB) runs without Postgres:

```bash
cd go-alert-service && go test ./services/ -v
```

## Example Requests

### Create an Alert Rule

```bash
curl -X POST \
  -H "Authorization: Bearer my-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"sensor_id":"sensor-001","name":"High Temp","operator":"gt","threshold":80.0}' \
  http://localhost:8081/rules
```

Response (201):
```json
{
  "id": "rule-001",
  "sensor_id": "sensor-001",
  "name": "High Temp",
  "operator": "gt",
  "threshold": 80.0,
  "metric": "value",
  "status": "active",
  "created_at": "2026-03-08T10:00:00Z",
  "updated_at": "2026-03-08T10:00:00Z"
}
```

### Trigger an Alert (Update Sensor Above Threshold)

```bash
curl -X PUT \
  -H "Authorization: Bearer my-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"value": 95.0}' \
  http://localhost:8080/sensors/sensor-001
```

This publishes a `sensor.updated` event. The alert service evaluates active rules and creates a triggered alert.

### List Triggered Alerts

```bash
curl -H "Authorization: Bearer my-secret-token" http://localhost:8081/alerts
```

### Acknowledge an Alert

```bash
curl -X PUT \
  -H "Authorization: Bearer my-secret-token" \
  -H "Content-Type: application/json" \
  -d '{"status": "acknowledged"}' \
  http://localhost:8081/alerts/alert-001
```

### Circuit Breaker Fallback Example

When the sensor service is unavailable, rule creation still succeeds with a warning:

```json
{
  "id": "rule-002",
  "sensor_id": "sensor-001",
  "name": "Fallback Rule",
  "operator": "lt",
  "threshold": 10.0,
  "status": "active",
  "warning": "Sensor service unavailable; sensor_id not validated"
}
```

## Resilience Features

| Feature | Location | Detail |
|---------|----------|--------|
| Circuit Breaker | Go Alert → Go Sensor | Opens after 5 consecutive failures; resets after 30s |
| HTTP Retry + Backoff | Go Alert → Go Sensor | Up to 3 attempts, exponential backoff (1s, 2s) |
| Request Timeout | Go Alert → Go Sensor | 2-second per-request timeout |
| Graceful Fallback | Rule creation | Creates rule with warning if sensor service unavailable |
| Publisher Resilience | Sensor → RabbitMQ | Publish failures logged/swallowed; auto-reconnect |
| Consumer Reconnect | Alert ← RabbitMQ | Auto-reconnects with 5-second backoff |

## Observability Evidence

All evidence captured from live Docker services (2026-04-01). Sensor `sensor-007` (temperature, 95.5°F) triggers rule `High Temp Alert` (threshold > 80°F).

### Structured Logs (Blocking Mode)

Services running with `PIPELINE_MODE=blocking`. Both Python (JSON) and Go (slog) emit structured logs with `trace_id` propagated from the sensor service through RabbitMQ to the alert evaluator.

![Blocking mode logs](results/screenshots/blocking-logs.png)

**Python sensor → Python alert (blocking):**
```
python-service-1  | {"timestamp": "2026-04-01T18:39:46", "level": "INFO", "message": "Sensor updated, publishing event", "sensor_id": "sensor-007", "trace_id": "46a9a39a-8742-46fb-a812-71a053ce9496"}
python-service-1  | {"timestamp": "2026-04-01T18:39:46", "level": "INFO", "message": "Published sensor.updated event", "sensor_id": "sensor-007", "value": 95.5, "trace_id": "46a9a39a-8742-46fb-a812-71a053ce9496"}
python-alert-1    | {"timestamp": "2026-04-01T18:39:46", "level": "INFO", "message": "Received sensor.updated event", "sensor_id": "sensor-007", "value": 95.5, "trace_id": "46a9a39a-8742-46fb-a812-71a053ce9496"}
python-alert-1    | {"timestamp": "2026-04-01T18:39:46", "level": "INFO", "message": "Alert triggered: Sensor sensor-007 value 95.5 gt threshold 80.0 (rule: High Temp Alert)", "trace_id": "46a9a39a-8742-46fb-a812-71a053ce9496", "alert_id": "alert-241930e0", "rule_id": "rule-004"}
```

**Go sensor → Go alert (blocking):**
```
go-service-1       | INFO Sensor updated, publishing event sensor_id=sensor-007 trace_id=7ee374e9-d574-41d4-b176-9b7036c5b41c
go-service-1       | INFO Published sensor.updated event sensor_id=sensor-007 value=95.5 trace_id=7ee374e9-d574-41d4-b176-9b7036c5b41c
go-alert-service-1 | INFO Received sensor.updated event sensor_id=sensor-007 value=95.5 trace_id=7ee374e9-d574-41d4-b176-9b7036c5b41c
go-alert-service-1 | INFO Alert triggered alert_id=alert-132944 rule_id=rule-006 trace_id=7ee374e9-d574-41d4-b176-9b7036c5b41c message="Sensor sensor-007 value 95.50 gt threshold 80.00 (rule: High Temp Alert)"
```

### Structured Logs (Reactive Mode)

Services restarted with `PIPELINE_MODE=async WORKER_COUNT=4`. The reactive pipeline (RxPY Subject / RxGo Observable) dispatches events to concurrent workers. Trace IDs remain consistent across the pipeline.

![Reactive mode logs](results/screenshots/reactive-logs.png)

**Python (reactive, 4 workers):**
```
python-alert-1 | {"timestamp": "2026-04-01T18:41:15", "level": "INFO", "message": "Alert consumer started", "mode": "reactive"}
python-alert-1 | {"timestamp": "2026-04-01T18:41:42", "level": "INFO", "message": "Received sensor.updated event", "sensor_id": "sensor-007", "value": 99.0, "trace_id": "2f22ba8b-0e14-439d-909e-3751a7e006a4"}
python-alert-1 | {"timestamp": "2026-04-01T18:41:42", "level": "INFO", "message": "Alert triggered: Sensor sensor-007 value 99.0 gt threshold 80.0 (rule: High Temp Alert)", "trace_id": "2f22ba8b-0e14-439d-909e-3751a7e006a4", "alert_id": "alert-3cb24f0c", "rule_id": "rule-004"}
```

**Go (reactive, 4 workers):**
```
go-alert-service-1 | INFO Alert consumer started mode=reactive
go-alert-service-1 | INFO Received sensor.updated event sensor_id=sensor-007 value=99 trace_id=703a87e8-4b72-47ec-b299-b9e40b6d5f63
go-alert-service-1 | INFO Alert triggered alert_id=alert-132946 rule_id=rule-006 trace_id=703a87e8-4b72-47ec-b299-b9e40b6d5f63 message="Sensor sensor-007 value 99.00 gt threshold 80.00 (rule: High Temp Alert)"
```

### Metrics Counters

Prometheus-compatible `/metrics` endpoint served on port 9090 (mapped to host 9091/9092). Counters increment with each event processed.

![Metrics counters](results/screenshots/metrics-counters.png)

The screenshot shows both services: Python alert service (top, port 9091) and Go alert service (bottom, port 9092).

**Python alert service (blocking, after 2 events):**
```
# HELP events_received_total Total sensor.updated events consumed from RabbitMQ.
# TYPE events_received_total counter
events_received_total 2

# HELP events_processed_total Total events that completed evaluation.
# TYPE events_processed_total counter
events_processed_total 2

# HELP alerts_triggered_total Total alerts triggered by threshold crossings.
# TYPE alerts_triggered_total counter
alerts_triggered_total 2

# HELP event_processing_avg_microseconds Average event processing duration in microseconds.
# TYPE event_processing_avg_microseconds gauge
event_processing_avg_microseconds 94965.0

# HELP pipeline_info Pipeline configuration.
# TYPE pipeline_info gauge
pipeline_info{mode="blocking",worker_count="4"} 1
```

**Go alert service (reactive, after 2 events):**
```
events_received_total 2
events_processed_total 2
alerts_triggered_total 2
event_processing_avg_microseconds 15637.3
pipeline_info{mode="async",worker_count="4"} 1
```

### Trace ID End-to-End

A single trace ID (`46a9a39a-8742-46fb-a812-71a053ce9496`) flowing through the full pipeline:

| Stage | Service | Log Entry |
|-------|---------|-----------|
| 1. Sensor PUT | python-service | `"Sensor updated, publishing event" ... trace_id=46a9a39a` |
| 2. RabbitMQ publish | python-service | `"Published sensor.updated event" ... trace_id=46a9a39a` |
| 3. Event consumed | python-alert | `"Received sensor.updated event" ... trace_id=46a9a39a` |
| 4. Alert triggered | python-alert | `"Alert triggered" ... trace_id=46a9a39a, alert_id=alert-241930e0` |

The same trace ID is also received by the Go alert service (cross-stack fanout via RabbitMQ):

| Stage | Service | Log Entry |
|-------|---------|-----------|
| 3. Event consumed | go-alert | `Received sensor.updated event ... trace_id=46a9a39a` |
| 4. Alert triggered | go-alert | `Alert triggered alert_id=alert-132943 ... trace_id=46a9a39a` |

## Architecture Decision Records

- [ADR-001](adrs/ADR-001-sync-vs-async.md) — Sync vs. async: why both are used
- [ADR-002](adrs/ADR-002-rabbitmq-selection.md) — Why RabbitMQ over Kafka/Redis Streams
- [ADR-003](adrs/ADR-003-circuit-breaker.md) — Why circuit breaker as the resilience pattern

## Environment Variables

### Go Alert Service

| Variable | Default | Description |
|----------|---------|-------------|
| `API_TOKEN` | *(required)* | Bearer token |
| `PORT` | `8081` | Service port |
| `DATABASE_DSN` | *(required)* | Postgres connection string |
| `SEED_DATA_PATH` | `/app/data/alert_rules.json` | Seed data |
| `SENSOR_SERVICE_URL` | `http://go-service:8080` | Sensor service URL |
| `RABBITMQ_URL` | *(required)* | RabbitMQ URL |
| `CB_FAIL_MAX` | `5` | Circuit breaker failure threshold |
| `CB_RESET_TIMEOUT` | `30` | Circuit breaker reset timeout (seconds) |
| `PIPELINE_MODE` | `blocking` | Pipeline mode: `blocking` or `async` |
| `WORKER_COUNT` | `4` | Worker pool size (used in async mode) |

## A3: Performance Analysis

### Pipeline Modes

The alert service supports two pipeline modes, configurable via `PIPELINE_MODE`:

![Pipeline Modes Diagram](diagrams/pipeline-modes.png)

```mermaid
---
title: "Pipeline Modes: Blocking vs Reactive"
---
flowchart LR
    subgraph Blocking["Blocking Mode"]
        direction TB
        B_RMQ["RabbitMQ\n(prefetch=10)"] -->|deliver| B_Consumer["Consumer\n(single coroutine/goroutine)"]
        B_Consumer -->|evaluate inline| B_DB[("SQLite")]
        B_DB -->|complete| B_Ack["Ack message"]
        B_Ack -->|next message| B_RMQ
    end

    subgraph Reactive["Reactive Mode (RxPY / RxGo)"]
        direction TB
        A_RMQ["RabbitMQ\n(prefetch=10)"] -->|deliver| A_Consumer["Consumer"]
        A_Consumer -->|"on_next(event)"| A_Subject["Subject / Observable"]
        A_Consumer -->|ack immediately| A_RMQ
        A_Subject -->|"flat_map\n(max_concurrent=N)"| A_W1["Worker 1"]
        A_Subject -->|"flat_map\n(max_concurrent=N)"| A_W2["Worker 2"]
        A_Subject -->|"flat_map\n(max_concurrent=N)"| A_WN["Worker N"]
        A_W1 -->|write| A_DB[("SQLite\n(serialized writes)")]
        A_W2 -->|write| A_DB
        A_WN -->|write| A_DB
    end
```

The default mode is `blocking`. **Blocking mode:** Messages are evaluated inline before being acknowledged — sequential, strong at-least-once guarantee. **Reactive mode:** Messages are emitted into an RxPY `Subject` (Python) or RxGo `Observable` (Go). The `flat_map` operator fans out evaluation to up to `WORKER_COUNT` concurrent tasks/goroutines. Backpressure is implemented at two levels: RabbitMQ's `prefetch=10` limits in-flight messages at the broker, and `flat_map`'s `max_concurrent` parameter caps concurrent evaluations at the application level — when all slots are occupied, new events wait until an evaluation completes.

### Load Test Results

The tests follow a progression: start with the simple blocking pipeline, observe its behavior as data scales, then transition to the reactive pipeline to see whether concurrency improves throughput.

#### Phase 1: Blocking (Sequential) Baseline

| Stack | Size | Throughput (events/s) | Avg Latency (µs) | Error Rate | CPU Peak | Memory |
|-------|------|-----------------------|-------------------|------------|----------|--------|
| Python | Small (50) | 33.0 | 29,233 | 0% | 0.25% | 44.2 MiB |
| Python | Medium (500) | 40.6 | 26,977 | 0% | 0.26% | 44.1 MiB |
| Python | Large (5000) | 37.4 | 30,312 | 0% | 0.27% | 44.1 MiB |
| Go | Small (50) | 63.4 | 56,919 | 0% | 50.17% | 13.6 MiB |
| Go | Medium (500) | 37.8 | 65,611 | 0% | 49.77% | 14.3 MiB |
| Go | Large (5000) | 15.1 | 86,977 | 0% | 50.25% | 14.6 MiB |

Python blocking throughput plateaus at ~37-41 events/s with stable latency (~27-30ms) and flat memory (~44 MiB). Go starts faster on small data (63.4 events/s) but degrades sharply at large scale (15.1 events/s) — it hits the Docker CPU limit (50% = 0.5 CPU cap) and the CGO overhead of `mattn/go-sqlite3` compounds with SQLite write serialization. Go's memory footprint is ~3x smaller than Python's.

#### Phase 2: Reactive Pipeline (RxPY / RxGo, 4 Workers)

| Stack | Size | Throughput (events/s) | Avg Latency (µs) | Error Rate | CPU Peak | Memory |
|-------|------|-----------------------|-------------------|------------|----------|--------|
| Python | Small (50) | 49.8 | 82,019 | 0% | 0.76% | 46.5 MiB |
| Python | Medium (500) | 40.4 | 537,874 | 0% | 0.15% | 56.7 MiB |
| Python | Large (5000) | 32.6 | 1,600,628 | 0% | 0.14% | 80.4 MiB |
| Go | Small (50) | 34.0 | 65,134 | 0% | 50.70% | 13.5 MiB |
| Go | Medium (500) | 40.3 | 71,142 | 0% | 50.65% | 14.4 MiB |
| Go | Large (5000) | 13.7 | 95,091 | 0% | 50.04% | 14.8 MiB |

The reactive pipeline shows **degrading performance at scale** in both stacks. Python's latency explodes from 82ms (small) to **1.6 seconds** (large) as the RxPY Subject and in-flight futures accumulate while workers contend on the database lock; memory nearly doubles (44→80 MiB). Go's reactive throughput is slightly worse than blocking at every size, with latency increasing modestly. Go's memory stays flat because RxGo's goroutine pool has minimal overhead compared to RxPY's future-per-event model.

### Analysis

The reactive pipeline is slower than blocking in both stacks. The bottleneck is SQLite: it serializes writes at the filesystem level, so the 4 reactive workers (dispatched via `flat_map`) contend on the same database file without achieving actual parallelism. Instead of speeding things up, the workers add lock contention overhead — more time is spent waiting for the database lock than doing useful work. Blocking mode avoids this entirely by processing events one at a time with no contention.

**Python** shows the most dramatic effect: the RxPY reactive pipeline creates an `asyncio.Future` per event and schedules evaluation through the `AsyncIOScheduler`. Under sustained load, futures pile up faster than SQLite can drain them — latency grows from 82ms to 1.6s, and memory climbs from 44 to 80 MiB. The blocking pipeline, by contrast, remains stable at ~30ms latency with flat memory.

**Go** hits the Docker CPU limit (0.5 CPU → 50% utilization) in both modes, which caps throughput. The reactive (RxGo) overhead is minimal — latency increases only modestly compared to blocking — because RxGo's `FlatMap` with `WithPool` maps directly to goroutines with no intermediate future allocation. Go's memory stays flat at ~14 MiB in both modes. The primary bottleneck in Go is the CPU cap combined with CGO overhead from `mattn/go-sqlite3`, not the reactive framework itself.

This result is specific to the choice of storage backend, not a flaw in the reactive pattern itself. In a production system, the storage layer would be a database that supports concurrent writes (e.g., PostgreSQL, MySQL, or a distributed store like DynamoDB). With such a backend, the reactive workers would perform truly parallel I/O, and the throughput advantage of the reactive pipeline would materialize. The general principle: **reactive pipelines improve throughput when the bottleneck is parallelizable I/O; when the bottleneck serializes all access through a single lock, concurrency adds overhead without benefit.**

As noted above, replacing SQLite with a connection-pooled database like PostgreSQL would allow the reactive workers to write in parallel, realizing the throughput gains that the pattern is designed for. A more interesting scenario is a pipeline where each event triggers multiple independent actions — for example, writing to the database, sending a notification, and updating a cache. In that case, even with SQLite, a reactive pipeline could perform the non-database actions in parallel with the database write and with each other. The scheduling overhead of the reactive framework is on the order of microseconds, which is negligible compared to the milliseconds saved by parallelizing I/O.

#### Message Delivery Guarantees

The reactive pipeline **ack**s messages before processing, which weakens message delivery guarantees. If a worker crashes after **ack** but before the database write completes, that event is lost silently — there is no built-in retry, error message, or warning. This is an at-most-once delivery model. The blocking pipeline, by contrast, **ack**s after processing, which provides at-least-once delivery for free. The tradeoff is intentional: **ack**-before-process is what allows the reactive pipeline to dispatch events to concurrent workers. In a system where the storage backend supports parallel writes, this tradeoff would pay off in throughput — but with SQLite serializing all writes, the throughput benefit never materializes, and the weaker delivery guarantee is cost without reward.

#### Python vs. Go Memory Growth
In the context of this reactive pipeline, the Python implementation sees significant memory growth as size increases, while the Go implementation does not. Specifically, from Small to Medium to Large (two 10x scale-ups), the Python memory usage increases from 46.5 MiB to 56.7 MiB to 80.4 MiB, nearly doubling. On the other hand, for Go, the same progression from Small to Medium to Large shows a very small memory increase from 13.5 MiB to 14.4 MiB to 14.8 MiB, a 9.6% increase in memory usage. For comparison, in the blocking case, Python's memory use hovers around 44-45 MiB (about the same as the Small reactive case), while Go's memory use hovers in the 13-15 MiB range with a very slight upward trend just as in the reactive case.

This is because every event in the Python implementation immediately creates an `asyncio.Future` object — each carrying roughly 400-500 bytes of overhead for the Future itself, its associated coroutine frame, and event data. While `merge(max_concurrent=4)` limits concurrent *execution* to 4 workers, the RxPY Subject still accepts all incoming events eagerly and wraps each one in a Future right away. Backpressure in Python is therefore *late* — it throttles execution but not object creation. When events arrive faster than SQLite can drain them, unexecuted Futures accumulate in the heap.

Go's design, on the other hand, applies backpressure *early*. `FlatMap` with `WithPool(4)` dispatches work to a fixed pool of 4 goroutines, and when all goroutines are busy, the channel blocks the producer — no intermediate object is created for the pending event. Since each goroutine is only 2-4 KB and the pool size is fixed, memory stays flat regardless of scale.

#### Impacts of Changing Storage Configuration
If SQLite were swapped for PostgreSQL, I would expect the Python Reactive throughput to increase substantially and latency to decline substantially, as the bottleneck in that case was the serialization of the asynchronous messages for database access. The Go reactive throughput would increase and latency would decrease, respectively, as write serialization is still inefficient in that case, though the CPU cap is the main bottleneck.
Memory usage would be largely unchanged. CPU usage in the Reactive cases would likely increase for Python, as workers would no longer be blocked waiting on SQLite's lock and would perform actual parallel work. All statistics in the Blocking cases (both Go and Python) would remain unchanged, as Blocking is sequential by design.

#### Debugging Considerations in a Reactive Pipeline
In a reactive pipeline, debugging is more difficult, as log outputs from multiple workers are interspersed and connecting a given log entry to the transaction that produced it is not straightforward. A stack trace in asynchronous/reactive code isn't as useful because futures and observables break the call chain on which stack trace-based analysis would depend. For example, without a trace_id, a reactive system with four workers would have nothing in an exception log to identify clearly which worker, let alone which transaction, was at fault. The trace_id solves this problem by connecting a given log entry to the transaction to which it pertains, allowing for debugging to proceed.

#### Backpressure Layers
The reactive architecture has three backpressure layers: broker-level (RabbitMQ pre-fetch), queue-level (`x-max-length=1000` with `reject-publish` overflow policy), and application-level (concurrent worker limit, implemented as `merge(max_concurrent=N)` in Python and channel + goroutine pool in Go). The queue depth cap ensures that even if the consumer falls behind, the queue cannot grow unboundedly — publishers receive a rejection when the queue is full, providing explicit back-pressure to upstream services.
RabbitMQ pre-fetch limits how many unacked messages RabbitMQ delivers to the consumer at once, while the concurrent worker limit restricts how many events are evaluated concurrently.
If RabbitMQ pre-fetch limits were removed, RabbitMQ would deliver all queued messages into the consumer's in-memory buffer, causing memory to grow when events arrive faster than they are written to SQLite. This would risk data loss if the system were to crash.
If the concurrent worker limit were removed, a greater number of workers would contend over the SQLite lock, increasing overhead. Even if SQLite were replaced with a parallel-friendly database, the worker limit would still protect the database's connection pool under high throughput.
The concurrent worker limit reduces lock contention on SQLite. RabbitMQ pre-fetch keeps unprocessed data in persistent broker storage, protecting it from application crashes.
The Python implementation has weaker overall backpressure because creating a Future is near-instant: from RabbitMQ's perspective, it looks the same as completing the work, so the broker never sees a reason to slow down delivery. The Go implementation, by contrast, blocks when the channel is full, which stalls the consumer and causes unacked messages to accumulate at the broker, triggering prefetch backpressure. Beyond preventing memory growth, the Go implementation reduces the risk of data loss by keeping unprocessed events in the crash-persistent message queue as long as possible.

#### Infrastructure Consideration
One more consideration in evaluating the two implementations here is the infrastructure demand. The Go configuration consistently hit the Docker CPU cap (cpus: 0.5, reported as ~50% utilization), with the blocking and reactive implementations both recording test results of 50%+/-1% CPU utilization across all three sizes. This shows that compute is likely the bottleneck in that implementation and similar Go microservices. In addition, `mattn/go-sqlite3` uses CGO, which adds marshalling overhead per call, and SQLite performs all SQL parsing and WAL writes locally on the container, both of which are CPU-intensive. Replacing SQLite with PostgreSQL would move the database computation to a separate server and obviate the need for CGO, eliminating both of the most compute-intensive factors.

On the other hand, as Python steadily grew in memory usage, RAM is and would continue to be one of its primary resource constraints at scale. Replacing SQLite with PostgreSQL would also move computation off the container, partially relieving both constraints. The Python implementation's compute utilization remained under 1% in all tests, with a narrow 0.25%-0.27% band for the blocking case. In the reactive case, the Small test shows 0.76% CPU while Medium and Large drop to 0.15% and 0.14% respectively: counterintuitively, more events result in *less* CPU usage. This is because at larger scale, workers spend most of their time suspended waiting for the SQLite lock (an I/O wait, not CPU work), while at small scale the RxPY scheduler and Future creation overhead are a larger proportion of the total work.

#### Conclusion
In short, the reactive architecture, when implemented properly, can outpace the blocking architecture substantially via parallelization. However, a database that serializes writes (like the SQLite used in the implementations examined here) negates that benefit while retaining the additional overhead that reactive architectures add. Moreover, reactive architectures like the ones implemented here introduce a small data loss risk with respect to **ack**ing before processing rather than after; however, such a risk is acceptable for most event-driven workloads but not for systems requiring guaranteed delivery. Lastly, each individual language's specific implementations of certain functionalities can add quirks that impact performance metrics peculiarly: for example, Python's use of Futures causes significant memory growth at scale in a reactive architecture, while operating SQLite on-container in Go necessitates the use of overhead-heavy CGO. These tradeoffs are context-dependent: the right architecture depends on the desired storage backend behavior, language runtime characteristics, and infrastructure constraints for a given deployment.

### Rerunning the Load Tests

The full blocking→async progression can be reproduced in a single command:

```bash
export API_TOKEN=my-secret-token
./scripts/load_test.sh --stack python --progression
```

This automatically:
1. Starts services in **blocking** mode, runs small → medium → large
2. Restarts services in **async** mode (4 workers), runs small → medium → large
3. Appends all results to `results/results.csv`

To run a single mode manually:

```bash
# Start in the desired mode
PIPELINE_MODE=blocking WORKER_COUNT=0 docker compose up -d --build
./scripts/load_test.sh --stack python

# Or async
PIPELINE_MODE=async WORKER_COUNT=4 docker compose up -d --build
./scripts/load_test.sh --stack python
```

### Deterministic Equivalence Test

To verify that both pipeline modes produce identical alert outputs for the same input:

```bash
export API_TOKEN=my-secret-token
./scripts/test_equivalence.sh python   # or: ./scripts/test_equivalence.sh go
```

This script automatically restarts services in blocking mode, sends a fixed set of sensor updates, collects triggered alerts, then repeats in reactive mode and compares the outputs. Both modes must produce the same number of alerts with the same rule_id, sensor_id, and threshold values.
