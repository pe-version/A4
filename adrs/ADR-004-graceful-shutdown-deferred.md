# ADR-004: Graceful Shutdown — Deferred for A4

**Date:** 2026-04-18
**Status:** Accepted

## Context

During SWEBoK/OWASP code review and subsequent dead-code cleanup, a `defer db.Close()` statement in both services' `main.go` was identified as non-functional: every error path exits via `os.Exit(1)` (which skips deferred functions) and `router.Run(addr)` blocks until the process is killed by SIGTERM (which also skips deferred functions). The defer was removed in commit `eabefb0` with an inline comment explaining the intentional absence.

The follow-up question was whether to implement proper graceful shutdown — trap SIGTERM, drain in-flight requests, close resources in order, exit cleanly — as the canonically correct fix. The answer was "not for A4." This ADR records that decision and the reasoning behind it.

Three options were evaluated:

| Option | What it means |
|--------|---------------|
| **A. Leave as-is (process dies on SIGTERM)** | Documented behavior, inline comment in main.go explains the absence |
| **B. Remove misleading defer only** | *(Done in commit `eabefb0`.)* Code matches actual behavior |
| **C. Implement full graceful shutdown** | Signal handling, ordered teardown of HTTP server + consumer + publisher + DB |

## Decision

Accept Option B (remove the dead defer; keep abrupt-exit behavior) for A4. Defer Option C (full graceful shutdown) indefinitely, to be revisited only if any of the preconditions under "When to reconsider" below are met.

## Rationale

### The current architecture is crash-tolerant by design

The A4 stack has three properties that together make abrupt termination operationally safe:

1. **Postgres is ACID.** Any transaction in flight at SIGTERM rolls back cleanly on the database side. The application cannot leave half-written rows.
2. **RabbitMQ uses manual ack with `autoAck=false`** in the alert consumer (see `go-alert-service/messaging/consumer.go`). Messages being evaluated at SIGTERM are never acked, so they are automatically requeued by the broker and redelivered to a surviving consumer. At-least-once delivery is preserved without any shutdown coordination on the application side.
3. **The application takes no long-lived locks, owns no outbox that needs draining, and has no reconciliation step that requires orderly exit.**

Chaos test 3 (`results/chaos/disconnect-queue.log`) already quantifies the one loss vector that does exist: fire-and-forget publish from the sensor service during an unavailable broker drops events. That risk exists regardless of graceful shutdown — a graceful shutdown that stops accepting new writes wouldn't change it, because the writes that would be dropped are new requests after the shutdown begins, not in-flight ones.

### The implementation surface is non-trivial

"Just trap SIGTERM" understates the work. Concrete touchpoints:

- **`go-service/main.go`** and **`go-alert-service/main.go`**: replace `router.Run(addr)` with an explicit `&http.Server{}` so `Shutdown(ctx)` is available; add `signal.NotifyContext` plumbing and an ordered shutdown cascade.
- **`go-service/messaging/publisher.go`**: add a `Close()` method (currently none exists) to release the AMQP channel and connection.
- **`go-alert-service/messaging/consumer.go`**: add a `Stop()` method (currently none). The current `consumeLoop()` is an unbounded `for {}` with `time.Sleep(5s)` for reconnect and an inner `for msg := range msgs` over the AMQP delivery channel. Making this context-aware requires threading a context through both loops and closing the broker connection from outside to break the inner range.
- **`go-alert-service/metrics/metrics.go`**: refactor `Serve(addr)` to return or expose an `*http.Server` so the metrics endpoint can be shut down too.
- **Alert service shutdown ordering**: metrics server → main HTTP server → consumer → DB (reversing this ordering is subtly wrong — closing DB first causes in-flight queries to fail with cryptic errors).

Estimated scope: ~200 lines, 5 files, plus tests. Shutdown tests are notoriously timing-sensitive and hard to make hermetic.

### Opportunity cost against the A4 rubric

Remaining A4 items with clearer grade payoff than graceful shutdown:

- Final Report (3–5 pages) — explicitly required, worth ~10 points
- Small/medium/large dataset load tests with CPU/memory — explicitly required, ~5 points
- First-person debugging story rewrite — 3 points
- Terminal screenshots — 2 points

Graceful shutdown is not mentioned in the rubric. The chaos/resilience section already scored 20/20 with the existing crash-tolerant design. Investing 2–4 hours in graceful shutdown would likely move zero rubric points.

### Implementation risks asymmetric with payoff

Bugs in shutdown logic manifest only at shutdown — the least-exercised code path. Typical failure modes include:

- Goroutine that doesn't honor context cancellation → service hangs at SIGTERM until Docker's SIGKILL (worse than no handler).
- Closing DB before HTTP server drains → in-flight queries fail with obscure errors.
- Reconnect-loop race with shutdown signal → 5-second delay before SIGTERM is noticed.
- Asymmetric implementation (one service clean, the other subtly wrong).

The payoff is cleaner shutdown logs. The cost is a class of bugs that are hard to diagnose.

## Consequences

### Accepted

- `docker compose down`, container restarts, and replica kills produce abrupt termination. HTTP clients see connection resets; RabbitMQ consumers are disconnected mid-message (which is fine — the message requeues).
- Exit logs do not show an orderly shutdown sequence. This is a cosmetic cost only; no correctness impact.
- The comment in `main.go` pointing to "graceful shutdown tracked separately" will remain a known placeholder until the decision is revisited.

### Not accepted (would make this decision wrong)

- Any introduction of in-memory state that needs draining (a write-behind cache, a bulk-insert buffer, a batch dispatcher).
- Any resource that requires orderly release (file locks, distributed locks, named pipes).
- Any deployment target that enforces PID 1 semantics strictly and penalizes non-zero exit codes from SIGTERM (some Kubernetes configurations).

## When to Reconsider

Implement graceful shutdown if any of the following become true:

1. **Rolling deployments under sustained load.** If the service is deployed to k8s/Swarm and gets restarted during traffic, abrupt termination will show as elevated 5xx rates on the client side during each deploy. Rolling deploy quality requires draining.
2. **A persistent outbox pattern is introduced.** If the sensor service's event publishing switches from fire-and-forget to a database-backed outbox (for at-least-once delivery across broker outages), draining the outbox on shutdown becomes important.
3. **An in-memory write buffer or batch processor is added** for any reason (e.g., batched metrics aggregation, coalesced DB writes).
4. **Client SLA requires zero connection resets during planned restarts.** For most use cases this is overkill; for some enterprise contracts it is literally stipulated.
5. **PCI, HIPAA, or similar compliance regime requires audit trail of orderly service stops.**
6. **The A4 codebase becomes a production or portfolio piece** where production hygiene matters independent of immediate operational need.

## Upgrade Path (If Revisited)

The following sketch captures what a correct implementation would look like. Not a complete design — a starting point.

### 1. Add `Close()` to `EventPublisher` (`go-service/messaging/publisher.go`)

```go
func (p *EventPublisher) Close() {
    if p.channel != nil {
        p.channel.Close()
    }
    if p.conn != nil {
        p.conn.Close()
    }
}
```

### 2. Add `Stop()` + context threading to `AlertConsumer` (`go-alert-service/messaging/consumer.go`)

Currently `consumeLoop()` is `for { consume() }`. Refactor to accept a context:

```go
func (c *AlertConsumer) consumeLoop(ctx context.Context) {
    for {
        if err := c.consume(ctx); err != nil {
            select {
            case <-ctx.Done():
                return
            case <-time.After(5 * time.Second):
            }
        }
    }
}
```

`consume(ctx)` needs to close its AMQP connection when the context is cancelled — that will cause the `for msg := range msgs` inner loop to exit, since the delivery channel closes when the connection drops. A `context.AfterFunc(ctx, func() { conn.Close() })` pattern works cleanly.

Expose `Stop()`:

```go
func (c *AlertConsumer) Stop() {
    c.cancel()  // cancel is stored from context.WithCancel when Start is called
}
```

### 3. Make `metrics.Serve` return the server (`go-alert-service/metrics/metrics.go`)

```go
func Serve(addr string) *http.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("/metrics", handler)
    srv := &http.Server{Addr: addr, Handler: mux}
    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            slog.Error("Metrics server failed", "error", err)
        }
    }()
    return srv
}
```

### 4. Rewire `main.go` in both services

Sensor service sketch:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: addr, Handler: router, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}

go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        slog.Error("Server error", "error", err)
        stop()
    }
}()

<-ctx.Done()
slog.Info("Shutdown signal received")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := srv.Shutdown(shutdownCtx); err != nil {
    slog.Error("HTTP shutdown error", "error", err)
}
publisher.Close()
db.Close()
slog.Info("Shutdown complete")
```

Alert service is the same pattern with one additional stage — stop the metrics server and consumer before closing DB:

```go
metricsSrv.Shutdown(shutdownCtx)
srv.Shutdown(shutdownCtx)
consumer.Stop()
db.Close()
```

### 5. Add HTTP server timeouts while you're there

Since `router.Run` is being replaced, add `ReadTimeout`/`WriteTimeout`/`IdleTimeout` (already a separate TODO item — combining the work is natural).

### 6. Verification plan

- `scripts/verify.sh` still passes 16/16.
- Manual test: `docker compose down` log output shows `"Shutdown signal received"` → `"Shutdown complete"` within ~5s, cleanly, from both Go services.
- Sustained-load test: run `scripts/scaling_test.sh` while executing `docker compose restart go-service` mid-run; expect zero 5xx responses from the LB (it should route to surviving replicas during the restart window).
- RabbitMQ message-in-flight test: trigger a sensor update, SIGTERM the alert service before it completes evaluation, verify the message appears redelivered on restart (should already pass because of `autoAck=false` — graceful shutdown shouldn't change this, but worth confirming).

## References

- Commit `eabefb0` — dead `defer db.Close()` removal
- `TODO.md` — Graceful shutdown entry under "Code Quality / Security Hardening"
- Analysis doc at `~/.claude/plans/majestic-sprouting-wind.md` (gitignored) — detailed options evaluation that preceded this ADR
