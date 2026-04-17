# Architecture Review — A4

> **Note on framing:** The rubric specifies that this review should be performed *before* making any A4 changes. Accordingly, Sections "Core Services" through "Failure Concerns" describe the **pre-A4 state** of the system (SQLite, no horizontal scaling, flat Docker network) and reason about what to change. The subsequent sections ("Language Selection for A4 Improvements" onward) describe the A4 implementation decisions that were made after this review. SQLite references in the early sections are intentional — they describe the state being reviewed, not the current state of the repository.

## Core Services

The core services of this system are a sensor service, currently simulating input but extensible to actual physical sensors providing data; and an alert service, allowing for the creation, editing, and deletion of alerts and the triggering of said alerts with relevant logging and messaging when the appropriate conditions/thresholds are breached.

## Bottlenecks

The RabbitMQ fanout exchange is an obvious bottleneck, as the alert service could fall behind in consumption if it is slower than the sensor service, and in that case the queue would back up. The write path in the sensor service could be a bottleneck under heavy load since a synchronous SQLite write is performed before publishing. Notably, the fanout exchange feeds four alert service consumers — Python and Go implementations each subscribing independently — which was a consequence of prior assignments requiring dual-language implementation rather than a deliberate architectural choice. The resulting redundancy is nevertheless a useful property, as it provides fault tolerance if one implementation falls behind or fails.

## Stateful vs Stateless

SQLite renders both services stateful. The HTTP handlers are stateless, but the persistence layer isn't, which affects scaling.

## Scaling Concerns

SQLite is the main scaling concern because it's file-based single-writer, so it can't scale horizontally without file locking issues.

## What I Would Refactor

SQLite was chosen for pragmatic reasons — the assignment constraints and explicit instructor confirmation of its appropriateness for this context — but even at that initial decision point it was clear that a production system would require something more robust and parallelizable, such as Postgres, whether as a full replacement or via a shared volume with WAL mode enabled.

## Failure Concerns

One serious concern is that of cascading failures of services. In order to mitigate that, if the sensor service is temporarily down, a circuit breaker is configured to return an error immediately to the alert service's synchronous HTTP calls after 5 consecutive failures of the sensor service to respond with a reset to half-open after 30 seconds to allow for one probe request. This mitigates the risk of having a failure of the sensor service block the alert service. The circuit breaker does NOT cover in-flight alerts during the open state, which would be lost. For a sensor service, fail-fast is better than indefinite blocking, and a guarantee of every delivery isn't too worrisome, as an alert will be triggered more than once if the relevant alert condition persists. Additionally, it doesn't itself cover the growth of the queue backlog. As for the asynchronous pipeline, the concern of blocking is irrelevant, so no such circuit breaker is necessary.

## Language Selection for A4 Improvements

All A4 scaling and hardening work is implemented in Go. The Go implementation was chosen over Python for the following reasons:

- **Throughput**: Go's Gin framework handles concurrent requests via goroutines natively, with lower per-request overhead than Python/FastAPI running under uvicorn
- **Latency consistency**: Go's garbage collector produces smaller, more predictable pauses than Python's, resulting in tighter p90/p99 latency numbers under load
- **Concurrency primitives**: Goroutines make the async worker pool explicit and cheaply tunable, which is directly relevant to pipeline mode experimentation
- **Memory efficiency**: Go service replicas consume significantly less RAM than Python equivalents, making it practical to run more replicas on a development machine
- **Instructor guidance**: The course explicitly indicated that full dual-language implementation was not required for A4 and that judgment should be used; ceteris paribus, a single language is simpler

The Python services remain in the stack as an unmodified baseline, carrying forward the A3 implementation. They continue to participate in the RabbitMQ fanout exchange but are not horizontally scaled and continue to use SQLite. To bring Python to parity with the Go implementation, the following would be required: replacing the synchronous `sqlite3` driver with `asyncpg` (Postgres, fully async), switching from a single uvicorn worker to Gunicorn with multiple uvicorn workers, and auditing all endpoint handlers to ensure no synchronous blocking calls occur within async paths. This work was not implemented as the Go path was sufficient to demonstrate the A4 objectives.

## Load Balancer Selection

Nginx was chosen as the load balancer for the Go services. The main options considered were:

- **Nginx**: Mature, extremely well-documented, minimal resource footprint, round-robin by default with straightforward upstream configuration. The right choice for a simple replicated service behind Docker Compose. No dynamic service discovery needed here since replica addresses are static within the Compose network.
- **Traefik**: Auto-discovers Docker containers via labels and dynamically updates routing as replicas come and go. More powerful, but introduces meaningful configuration complexity and a separate dashboard process. Most valuable when replica counts change at runtime (e.g., Kubernetes or Docker Swarm). Overkill for a fixed-replica experiment.
- **HAProxy**: Industry-standard TCP/HTTP load balancer with rich health check and stats features. More configurable than Nginx for advanced scenarios (least-connections, session persistence), but heavier to configure for a basic round-robin use case.
- **Envoy / Istio**: Service mesh options offering advanced routing (canary deployments, A/B testing, mTLS between services). Substantial operational overhead; suited to large-scale production Kubernetes environments, not a Docker Compose experiment.

For this assignment, Nginx's simplicity and the static nature of the replica set made it the obvious choice. If this system were deployed to a container orchestrator with dynamic scaling, Traefik would be the natural upgrade path.

### Balancing Algorithm

Round-robin is the default and is appropriate here because the Go services are stateless — every replica can handle any request equivalently, and no session affinity is needed since all state lives in the shared Postgres database. If load testing reveals uneven performance (e.g., some requests are disproportionately slow), `least_conn` would be a worthwhile alternative, sending each new request to whichever replica has the fewest active connections. IP-hash (sticky sessions) is unnecessary and would actually undermine the scaling benefit by pinning clients to replicas.

### Health Checking

Nginx (open-source) uses passive health checking: it does not actively probe upstreams, but instead detects failures when a request to a replica fails (connection refused, timeout). On failure, Nginx marks the upstream as unavailable and routes subsequent requests to healthy replicas. The first request that hits a crashed replica will see a brief error or retry delay before Nginx reroutes it.

This is acceptable for several reasons:

1. **Docker Compose already handles restarts.** Each Go service has `restart: unless-stopped` and a `healthcheck` configured. If a replica crashes, Docker will restart it. Nginx's passive detection bridges the brief gap until the replacement is ready.
2. **The failure window is extremely small.** In practice, Nginx detects a dead upstream on the very first failed connection attempt — typically within milliseconds, not seconds. The "passive" label makes it sound slower than it is. Active health checks poll on an interval (e.g., every 5 seconds), meaning they can actually be *slower* to detect a failure that happens right after a probe succeeds.
3. **Retries absorb the impact.** Nginx's default behavior on a failed upstream is to try the next one in the round-robin. From the client's perspective, a single dead replica adds one failed connection attempt (sub-millisecond) before the request is served by another replica. Under normal load this is invisible.
4. **Active health checks solve a different problem.** They are most valuable when you need to proactively remove replicas that are degraded but still accepting connections (e.g., returning 500s, responding slowly). A fully crashed replica is the easy case — the connection simply fails and Nginx moves on immediately. Active checks shine in slow-degradation scenarios, which are less likely in a stateless Go service than in, say, a JVM application with GC pressure.
5. **Nginx Plus (paid) and Traefik offer active checks**, but adding either would increase cost or complexity for marginal benefit in a fixed-replica Docker Compose environment where failure windows are brief and recovery is automatic.

In a production deployment with SLA requirements, active health checking (via Traefik, HAProxy, or Nginx Plus) would be the right call. For this assignment's scope, passive failover provides sufficient resilience without additional infrastructure complexity.

### DNS Resolution

Nginx resolves upstream DNS names at startup and caches the result. If replicas are scaled *after* Nginx is already running, Nginx will not discover the new ones until restarted. This is not an issue for the current workflow — `docker compose up --build` with the replica count set via environment variables starts everything together. If dynamic scaling were needed, adding `resolver 127.0.0.11 valid=10s;` to the Nginx configuration would force periodic re-resolution against Docker's internal DNS. This is another area where Traefik handles the problem automatically.

### Metrics and Observability Through the Load Balancer

With multiple replicas, each Go alert service instance runs its own Prometheus metrics endpoint on `:9090`. The load balancer exposes a single port, which means a scrape request through the LB hits a different replica each time — mixing counters from different instances into an incoherent time series. In production, the correct approach is a Prometheus service discovery configuration (e.g., `dockersd_configs` or Consul) that discovers all replica IPs and scrapes each one directly, bypassing the LB entirely.

For this assignment, direct-scrape Prometheus configuration is not implemented. The reasons:

1. **The goal is to demonstrate scaling behavior, not build a production monitoring stack.** Adding Prometheus with Docker service discovery introduces a new container, a scrape configuration file, and a dependency on Docker socket access — meaningful complexity for a component that is not itself being graded.
2. **Per-replica metrics are still accessible.** Docker assigns each replica an IP on the Compose network; `docker compose ps` and `docker network inspect` expose these addresses. Metrics can be curled directly from any replica for spot checks and screenshots.
3. **Aggregate behavior is observable without per-replica scraping.** The load test measures end-to-end throughput and latency from the client's perspective (through the LB), which is the metric that actually matters for the scaling experiment. Whether replica A handled 60% of requests and replica B handled 40% is interesting but secondary to "did total throughput increase with more replicas?"
4. **Log-based observability fills the gap.** Each replica emits structured logs with its own container ID. Aggregating logs (`docker compose logs go-service`) shows per-replica activity interleaved chronologically, which is sufficient to verify that load is distributed and all replicas are active.

The strongest argument *for* direct Prometheus scraping in this use case: the chaos testing section requires killing a replica and observing what happens. With per-replica metrics, you could show a graph where one replica's request counter flatlines at the exact moment it was killed while the others absorb the load — a compelling visual that's impossible to produce when scraping through the LB. Log analysis can demonstrate the same thing, but a Prometheus graph tells the story more immediately. This tradeoff was accepted in favor of simplicity.
